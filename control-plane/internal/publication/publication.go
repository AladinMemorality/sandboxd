// Package publication keeps complete production frontends outside the guest.
// The only mutable pointer is current; readers see either complete revision.
package publication

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

var ErrUnsupported = errors.New("no supported production frontend")
var identifier = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

const maxBytes = 64 << 20
const maxFiles = 1024

type Source interface {
	ListFiles(context.Context, string, bool) (*runtime.FileList, error)
	ReadFile(context.Context, string) ([]byte, error)
}

func validPath(p string) bool {
	if p == "" || p != path.Clean(p) || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return !strings.HasSuffix(p, ".map")
}

// Empty Git directory placeholders are build metadata, not frontend assets.
// Validate the parent too: a placeholder must never hide traversal or dot paths.
func ignoredBuildFile(name string, size int64) bool {
	parent := path.Dir(name)
	return size == 0 && path.Base(name) == ".gitkeep" &&
		name == path.Clean(name) && (parent == "." || validPath(parent))
}

func Current(root, id string) (string, error) {
	if root == "" || !identifier.MatchString(id) {
		return "", os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join(root, id, "current"))
	if err != nil {
		return "", err
	}
	revision := string(data)
	if !identifier.MatchString(revision) {
		return "", os.ErrInvalid
	}
	_, err = os.Stat(filepath.Join(root, id, revision+".zip"))
	return revision, err
}

// Capture accepts the Vite SPA output contract only. SSR/custom builds retain
// live delivery. No project code is executed on the controller.
func Capture(ctx context.Context, root, id, revision string, source Source, beforeCommit func() error) error {
	if root == "" || !identifier.MatchString(id) || !identifier.MatchString(revision) {
		return os.ErrInvalid
	}
	if current, err := Current(root, id); err == nil && current >= revision {
		return nil
	}
	pkg, err := source.ReadFile(ctx, "package.json")
	if err != nil {
		return err
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(pkg, &manifest) != nil || (manifest.Dependencies["vite"] == "" && manifest.DevDependencies["vite"] == "") {
		return ErrUnsupported
	}
	list, err := source.ListFiles(ctx, "dist", true)
	if err != nil {
		return err
	}
	if len(list.Entries) > maxFiles {
		return ErrUnsupported
	}
	files := map[string][]byte{}
	size := 0
	for _, e := range list.Entries {
		if e.Type == "dir" {
			continue
		}
		name := strings.TrimPrefix(e.Path, "dist/")
		if name != e.Path && e.Type == "file" && ignoredBuildFile(name, e.Size) {
			continue
		}
		if name == e.Path || !validPath(name) {
			return ErrUnsupported
		}
		if e.Type != "file" || e.Size < 0 || e.Size > runtime.MaxFileContentBytes {
			return ErrUnsupported
		}
		if _, exists := files[name]; exists {
			return ErrUnsupported
		}
		data, err := source.ReadFile(ctx, e.Path)
		if err != nil {
			return err
		}
		if int64(len(data)) != e.Size {
			return errors.New("build changed during capture")
		}
		size += len(data)
		if size > maxBytes {
			return ErrUnsupported
		}
		files[name] = data
	}
	index := files["index.html"]
	if len(index) == 0 || bytes.Contains(index, []byte("/@vite/client")) || bytes.Contains(index, []byte(`src="/src/`)) {
		return ErrUnsupported
	}
	// Recheck the bytes to reject an independent build overwriting dist during
	// the copy. The promotion callback separately fences newer platform edits.
	for name, data := range files {
		again, err := source.ReadFile(ctx, "dist/"+name)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, again) {
			return errors.New("build changed during capture")
		}
	}
	dir := filepath.Join(root, id)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".build-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	archive := zip.NewWriter(f)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		if _, err = entry.Write(files[name]); err != nil {
			return err
		}
	}
	if err = archive.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// The caller serializes only this final promotion, never the remote copy.
	// Re-check generation here so a newer completed build cannot be replaced
	// by an older transfer that happened to finish later.
	if beforeCommit != nil {
		if err = beforeCommit(); err != nil {
			return err
		}
	}
	if current, e := Current(root, id); e == nil && current >= revision {
		return nil
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, revision+".zip")); err != nil {
		return err
	}
	pointer, err := os.CreateTemp(dir, ".current-")
	if err != nil {
		return err
	}
	defer os.Remove(pointer.Name())
	defer pointer.Close()
	if _, err = pointer.WriteString(revision); err != nil {
		return err
	}
	if err = pointer.Sync(); err != nil {
		return err
	}
	if err = pointer.Close(); err != nil {
		return err
	}
	if err = os.Rename(pointer.Name(), filepath.Join(dir, "current")); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return err
	}
	// Bound storage to five successful revisions per sandbox (at most 320 MiB
	// uncompressed). Retained revisions keep old tabs' lazy chunks available.
	revisions, _ := filepath.Glob(filepath.Join(dir, "*.zip"))
	sort.Sort(sort.Reverse(sort.StringSlice(revisions)))
	for i, old := range revisions {
		if i >= 5 && old != filepath.Join(dir, revision+".zip") && identifier.MatchString(strings.TrimSuffix(filepath.Base(old), ".zip")) {
			_ = os.Remove(old)
		}
	}
	return nil
}

// Serve returns false for backend requests. The existing authenticated preview
// proxy handles those and wakes the guest only when backend work is needed.
func Serve(w http.ResponseWriter, r *http.Request, root, id string) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	if r.Header.Get("Upgrade") != "" || r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/graphql" || strings.HasPrefix(r.URL.Path, "/socket.io/") {
		return false
	}
	revision, err := Current(root, id)
	if err != nil {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if name == "" {
		name = "index.html"
	}
	if !validPath(name) {
		http.NotFound(w, r)
		return true
	}
	// Pin script/chunk imports to a complete revision while users keep old tabs
	// open across publishes. These URLs still require the app's auth cookie.
	const prefix = "__sandboxd/build/"
	if strings.HasPrefix(name, prefix) {
		parts := strings.SplitN(strings.TrimPrefix(name, prefix), "/", 2)
		if len(parts) != 2 || !identifier.MatchString(parts[0]) || !validPath(parts[1]) {
			http.NotFound(w, r)
			return true
		}
		revision, name = parts[0], parts[1]
	}
	archive, err := zip.OpenReader(filepath.Join(root, id, revision+".zip"))
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	defer archive.Close()
	entries := map[string]*zip.File{}
	for _, f := range archive.File {
		entries[f.Name] = f
	}
	file := entries[name]
	if file == nil {
		if strings.HasPrefix(r.URL.Path, "/__sandboxd/") || strings.HasPrefix(r.URL.Path, "/assets/") {
			http.NotFound(w, r)
			return true
		}
		// Client-side navigation gets the SPA document; API fetches do not.
		if r.Header.Get("Sec-Fetch-Mode") != "navigate" || !strings.Contains(r.Header.Get("Accept"), "text/html") || path.Ext(name) != "" {
			return false
		}
		name, file = "index.html", entries["index.html"]
	}
	if file == nil || file.UncompressedSize64 > runtime.MaxFileContentBytes {
		http.NotFound(w, r)
		return true
	}
	reader, err := file.Open()
	if err != nil {
		http.Error(w, "build unavailable", 503)
		return true
	}
	data, err := io.ReadAll(io.LimitReader(reader, runtime.MaxFileContentBytes+1))
	reader.Close()
	if err != nil || len(data) > runtime.MaxFileContentBytes {
		http.Error(w, "build unavailable", 503)
		return true
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if name == "index.html" {
		// Vite writes absolute entry URLs and relative chunk imports. Only rewrite
		// paths that actually exist in this build, preserving application routes.
		for asset := range entries {
			if asset == "index.html" {
				continue
			}
			for _, quote := range []string{`"`, `'`} {
				data = bytes.ReplaceAll(data, []byte(quote+"/"+asset+quote), []byte(quote+"/"+prefix+revision+"/"+asset+quote))
			}
		}
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	sum := sha256.Sum256(data)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	return true
}
