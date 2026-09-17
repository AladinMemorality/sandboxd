package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"golang.org/x/sys/unix"
)

func (a *app) handleSourceExport(w http.ResponseWriter, r *http.Request) {
	data, err := scopedExportFiltered(r.Context(), a.appDir, true)
	if err != nil {
		scopedError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Write(data)
}

// Import is a whole-source replacement, not ZIP extraction into an existing
// tree. Validate/decompress first, build a clean sibling, atomically exchange it
// with the app directory, then restart the supervisor to reload sandbox.yaml.
// No credential/config directory outside the app root is copied or changed.
func (a *app) handleSourceImport(w http.ResponseWriter, r *http.Request) {
	if a.requestRestart == nil {
		http.Error(w, "source import requires a restart-capable supervisor", 503)
		return
	}
	if r.ContentLength > runtime.MaxWorkspaceExportBytes {
		scopedError(w, errScopedLimit)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, runtime.MaxWorkspaceExportBytes))
	if err != nil {
		scopedError(w, errScopedLimit)
		return
	}
	clean, err := runtime.SanitizeSourceArchive(data)
	if err != nil {
		http.Error(w, "invalid source archive", 400)
		return
	}
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	if a.restartPending {
		http.Error(w, "supervisor restarting", 409)
		return
	}
	if a.task != nil {
		a.task.mu.Lock()
		running := !a.task.done
		a.task.mu.Unlock()
		if running {
			http.Error(w, "task is running", 409)
			return
		}
	}
	if err = replaceSource(a.appDir, clean); err != nil {
		if errors.Is(err, errDependencyMismatch) {
			http.Error(w, "source dependency manifests differ from the prepared template; rebuild the template dependencies before importing", 409)
			return
		}
		scopedError(w, err)
		return
	}
	a.restartPending = true
	writeJSON(w, 200, map[string]bool{"imported": true, "restarting": true})
	if flush, ok := w.(http.Flusher); ok {
		flush.Flush()
	}
	// Give the response a chance to drain before closing the listeners. The new
	// process starts with the same private transport credential, never from disk.
	go func() { time.Sleep(100 * time.Millisecond); a.requestRestart() }()
}
func replaceSource(root string, data []byte) error {
	dir, err := openScopedRoot(filepath.Dir(root))
	if err != nil {
		return err
	}
	defer dir.Close()
	leaf := filepath.Base(root)
	original, err := openChild(dir, leaf, true)
	if err != nil {
		return err
	}
	defer original.Close()
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	name := ".source-import-" + hex.EncodeToString(random)
	if err = unix.Mkdirat(int(dir.Fd()), name, 0755); err != nil {
		return err
	}
	defer removeSourceTree(dir, name, 0)
	staged := filepath.Join(filepath.Dir(root), name)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		reader, err := f.Open()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(io.LimitReader(reader, runtime.MaxFileWriteBytes+1))
		reader.Close()
		if err != nil {
			return err
		}
		if err = scopedWrite(staged, f.Name, content); err != nil {
			return err
		}
	}
	// Reuse only the fresh destination template's dependencies, never creator
	// node_modules. Exact package/lockfile agreement is mandatory; mismatch
	// requires a separately reviewed dependency build rather than a broken app.
	stagedDir, e := openChild(dir, name, true)
	if e != nil {
		return e
	}
	defer stagedDir.Close()
	movedDependencies := false
	var deps unix.Stat_t
	if e = unix.Fstatat(int(original.Fd()), "node_modules", &deps, unix.AT_SYMLINK_NOFOLLOW); e == nil {
		if deps.Mode&unix.S_IFMT != unix.S_IFDIR && deps.Mode&unix.S_IFMT != unix.S_IFLNK {
			return errScopedPath
		}
		same, e := sameDependencyManifest(original, stagedDir)
		if e != nil {
			return e
		}
		if !same {
			return errDependencyMismatch
		}
		if e = unix.Renameat(int(original.Fd()), "node_modules", int(stagedDir.Fd()), "node_modules"); e != nil {
			return e
		}
		movedDependencies = true
	} else if !errors.Is(e, unix.ENOENT) {
		return e
	}
	// Renameat2 exchange never follows a leaf symlink and cannot expose a partial
	// tree. The old tree remains under the private staging name until cleanup.
	err = unix.Renameat2(int(dir.Fd()), name, int(dir.Fd()), leaf, unix.RENAME_EXCHANGE)
	if err != nil && movedDependencies {
		_ = unix.Renameat(int(stagedDir.Fd()), "node_modules", int(original.Fd()), "node_modules")
	}
	return err
}
func removeSourceTree(parent *os.File, name string, depth int) error {
	if depth > 64 {
		return errScopedLimit
	}
	child, err := openChild(parent, name, true)
	if err != nil {
		return unix.Unlinkat(int(parent.Fd()), name, 0)
	}
	defer child.Close()
	for {
		names, err := child.Readdirnames(128)
		if err != nil && err != io.EOF {
			return err
		}
		for _, entry := range names {
			if e := removeSourceTree(child, entry, depth+1); e != nil {
				return e
			}
		}
		if err == io.EOF {
			break
		}
	}
	return unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
}

var errDependencyMismatch = errors.New("source dependency manifests differ from the prepared template")

func sameDependencyManifest(original, staged *os.File) (bool, error) {
	names := []string{"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb", "pnpm-workspace.yaml", ".yarnrc.yml"}
	for _, name := range names {
		left, le := dependencyManifest(original, name)
		right, re := dependencyManifest(staged, name)
		if errors.Is(le, os.ErrNotExist) && errors.Is(re, os.ErrNotExist) {
			if name == "package.json" {
				return false, nil
			}
			continue
		}
		if errors.Is(le, os.ErrNotExist) || errors.Is(re, os.ErrNotExist) {
			return false, nil
		}
		if le != nil {
			return false, le
		}
		if re != nil {
			return false, re
		}
		if !bytes.Equal(left, right) {
			return false, nil
		}
	}
	return true, nil
}
func dependencyManifest(dir *os.File, name string) ([]byte, error) {
	f, err := openChild(dir, name, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errScopedPath
	}
	b, err := io.ReadAll(io.LimitReader(f, runtime.MaxFileReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > runtime.MaxFileReadBytes {
		return nil, errScopedLimit
	}
	return b, nil
}
