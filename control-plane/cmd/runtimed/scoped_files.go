package main

// Descriptor-relative opens pin each directory and refuse symlinks at every
// component. Never re-open a validated absolute pathname: workspace processes
// may concurrently replace directories with links.
import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"golang.org/x/sys/unix"
)

var errScopedPath = errors.New("invalid or excluded workspace path")
var errScopedLimit = errors.New("workspace operation exceeds limit")
var fileExclusions = map[string]bool{"node_modules": true, ".git": true, "dist": true, ".vite": true, ".next": true, ".runtimed": true, "lost+found": true}

func scopedParts(raw string, allowRoot, exclude bool) ([]string, error) {
	if len(raw) > 4096 || strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "\x00\\") {
		return nil, errScopedPath
	}
	// The platform file browser requests path=.; accept that exact root alias
	// for listings only. Embedded dot components remain invalid everywhere.
	if (raw == "" || raw == ".") && allowRoot {
		return nil, nil
	}
	parts := strings.Split(raw, "/")
	if len(parts) > 32 {
		return nil, errScopedLimit
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || (exclude && fileExclusions[p]) {
			return nil, errScopedPath
		}
	}
	return parts, nil
}
func openScopedRoot(root string) (*os.File, error) {
	if !strings.HasPrefix(root, "/") {
		return nil, errScopedPath
	}
	parts, err := scopedParts(strings.TrimPrefix(root, "/"), false, false)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), "/")
	for _, part := range parts {
		next, err := openChild(dir, part, true)
		dir.Close()
		if err != nil {
			return nil, err
		}
		dir = next
	}
	return dir, nil
}
func openChild(dir *os.File, name string, isDir bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if isDir {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(dir.Fd()), name, flags, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		f.Close()
		return nil, err
	}
	mode := st.Mode & unix.S_IFMT
	if (isDir && mode != unix.S_IFDIR) || (!isDir && mode != unix.S_IFREG && mode != unix.S_IFDIR) || (mode == unix.S_IFREG && st.Nlink != 1) {
		f.Close()
		return nil, errScopedPath
	}
	return f, nil
}
func scopedDir(root string, parts []string, mkdir bool) (*os.File, error) {
	dir, err := openScopedRoot(root)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		next, err := openChild(dir, part, true)
		if errors.Is(err, unix.ENOENT) && mkdir {
			if e := unix.Mkdirat(int(dir.Fd()), part, 0755); e != nil && !errors.Is(e, unix.EEXIST) {
				dir.Close()
				return nil, e
			}
			next, err = openChild(dir, part, true)
		}
		dir.Close()
		if err != nil {
			return nil, err
		}
		dir = next
	}
	return dir, nil
}
func scopedRead(root, raw string, limit int64, exclude bool) ([]byte, error) {
	parts, err := scopedParts(raw, false, exclude)
	if err != nil {
		return nil, err
	}
	dir, err := scopedDir(root, parts[:len(parts)-1], false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := openChild(dir, parts[len(parts)-1], false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errScopedPath
	}
	if st.Size() > limit {
		return nil, errScopedLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errScopedLimit
	}
	return data, nil
}
func scopedWrite(root, raw string, data []byte) error {
	parts, err := scopedParts(raw, false, true)
	if err != nil {
		return err
	}
	if len(data) > runtime.MaxFileWriteBytes {
		return errScopedLimit
	}
	dir, err := scopedDir(root, parts[:len(parts)-1], true)
	if err != nil {
		return err
	}
	defer dir.Close()
	name := parts[len(parts)-1]
	// O_NOFOLLOW rejects existing special leaves. Rename never follows a leaf
	// installed after this check, so a racing symlink cannot redirect the write.
	if existing, e := openChild(dir, name, false); e == nil {
		st, e := existing.Stat()
		existing.Close()
		if e != nil || !st.Mode().IsRegular() {
			return errScopedPath
		}
	} else if !errors.Is(e, unix.ENOENT) {
		return e
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	tmp := ".put-" + hex.EncodeToString(random)
	fd, err := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	defer f.Close()
	defer unix.Unlinkat(int(dir.Fd()), tmp, 0)
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(0644); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return unix.Renameat(int(dir.Fd()), tmp, int(dir.Fd()), name)
}

// scopedWalk visits opened files, not pathname lookups. Entries, depth, and
// observed directory names (including skipped entries) are bounded.
func scopedWalk(ctx context.Context, dir *os.File, prefix string, recursive bool, depth int, seen *int, visit func(string, *os.File, os.FileInfo) error) error {
	if depth > 32 {
		return errScopedLimit
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		names, err := dir.Readdirnames(128)
		if err != nil && err != io.EOF {
			return err
		}
		for _, name := range names {
			*seen++
			if *seen > runtime.MaxWorkspaceEntries {
				return errScopedLimit
			}
			if fileExclusions[name] || strings.HasPrefix(name, ".put-") {
				continue
			}
			child, e := openChild(dir, name, false)
			if e != nil {
				continue
			}
			info, e := child.Stat()
			if e != nil {
				child.Close()
				return e
			}
			rel := path.Join(prefix, name)
			if len(rel) > 4096 {
				child.Close()
				return errScopedLimit
			}
			e = visit(rel, child, info)
			if e == nil && info.IsDir() && recursive {
				e = scopedWalk(ctx, child, rel, recursive, depth+1, seen, visit)
			} else if e == fs.SkipDir && info.IsDir() {
				e = nil
			}
			child.Close()
			if e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}
func scopedList(ctx context.Context, root, raw string, recursive bool) (*runtime.FileList, error) {
	parts, err := scopedParts(raw, true, true)
	if err != nil {
		return nil, err
	}
	dir, err := scopedDir(root, parts, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	out := &runtime.FileList{Path: raw, Recursive: recursive, Entries: []runtime.FileEntry{}}
	seen := 0
	err = scopedWalk(ctx, dir, raw, recursive, 0, &seen, func(rel string, f *os.File, info os.FileInfo) error {
		typ := "file"
		if info.IsDir() {
			typ = "dir"
		}
		out.Entries = append(out.Entries, runtime.FileEntry{Path: rel, Type: typ, Size: info.Size()})
		return nil
	})
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out, err
}

type cappedZipBuffer struct{ bytes.Buffer }

func (b *cappedZipBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > runtime.MaxWorkspaceExportBytes {
		return 0, errScopedLimit
	}
	return b.Buffer.Write(p)
}
func scopedExport(ctx context.Context, root string) ([]byte, error) {
	return scopedExportFiltered(ctx, root, false)
}
func scopedExportFiltered(ctx context.Context, root string, published bool) ([]byte, error) {
	dir, err := openScopedRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var out cappedZipBuffer
	zw := zip.NewWriter(&out)
	seen := 0
	var total int64
	err = scopedWalk(ctx, dir, "", true, 0, &seen, func(rel string, f *os.File, info os.FileInfo) error {
		if published && info.IsDir() && !runtime.PublishedSourcePath(rel+"/source.js") {
			return fs.SkipDir
		}
		if info.IsDir() || (published && !runtime.PublishedSourcePath(rel)) {
			return nil
		}
		if info.Size() > runtime.MaxFileWriteBytes {
			return errScopedLimit
		}
		if total+info.Size() > runtime.MaxWorkspaceExportBytes {
			return errScopedLimit
		}
		writer, e := zw.Create(rel)
		if e != nil {
			return e
		}
		limit := int64(runtime.MaxFileWriteBytes)
		if remaining := int64(runtime.MaxWorkspaceExportBytes) - total; remaining < limit {
			limit = remaining
		}
		n, e := io.Copy(writer, io.LimitReader(f, limit+1))
		total += n
		if n > limit {
			return errScopedLimit
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
