package api

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File reads are served by sandboxd directly from the host-side
// workspace loopback mount — so
// they work whether or not the sandbox is running, and runtimed is
// not on the path.

const (
	appSubdir    = "workspace/app"
	maxFileBytes = 2 << 20 // 2 MiB cap on a single file read
)

// excludedFromFiles are never listed, read, or exported.
var excludedFromFiles = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, ".vite": true,
}

func (s *Server) appDirFor(id string) string {
	_, mnt := s.Loopback.Paths(id)
	return filepath.Join(mnt, appSubdir)
}

// safeJoin resolves a caller-supplied path under root, rejecting any
// escape (`..`, absolute paths) LEXICALLY. Callers that then open the
// path must use the descriptor-based helpers in v1_files_secure_linux.go;
// this lexical check alone cannot constrain tenant-controlled symlinks.
func safeJoin(root, p string) (string, bool) {
	full := filepath.Join(root, filepath.Clean("/"+p))
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", false
	}
	return full, true
}

type fileEntry struct {
	Path string `json:"path"` // relative to the app dir
	Type string `json:"type"` // "file" | "dir"
	Size int64  `json:"size,omitempty"`
}

// --- GET /v1/sandboxes/{id}/files -----------------------------------

func (s *Server) v1ListFiles(w http.ResponseWriter, r *http.Request) {
	if s.serveCubeFiles(w, r, "v1ListFiles") {
		return
	}
	id := r.PathValue("id")
	if !isULID(id) {
		writeV1Err(w, http.StatusNotFound, "not_found", "no such directory")
		return
	}
	root := s.appDirFor(id)
	p := r.URL.Query().Get("path")
	recursive := r.URL.Query().Get("recursive") == "true"
	full, ok := safeJoin(root, p)
	if !ok {
		writeV1Err(w, http.StatusBadRequest, "invalid_request", "invalid path")
		return
	}
	rel, err := filepath.Rel(root, full)
	_, mnt := s.Loopback.Paths(id)
	dir, err := openAppRead(mnt, rel, true)
	if err != nil {
		writeV1Err(w, http.StatusNotFound, "not_found", "no such directory")
		return
	}
	defer dir.Close()
	var entries []fileEntry
	prefix := rel
	if prefix == "." {
		prefix = ""
	}
	err = walkAppFiles(int(dir.Fd()), prefix, recursive, func(path string, isDir bool, file *os.File) error {
		e := fileEntry{Path: path, Type: "dir"}
		if !isDir {
			st, err := file.Stat()
			if err != nil {
				return err
			}
			e.Type, e.Size = "file", st.Size()
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		writeV1Err(w, http.StatusInternalServerError, "internal", "unable to list directory")
		return
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	writeJSON(w, http.StatusOK, map[string]any{
		"path": p, "recursive": recursive, "entries": entries,
	})
}

// --- GET /v1/sandboxes/{id}/files/content ---------------------------

func (s *Server) v1FileContent(w http.ResponseWriter, r *http.Request) {
	if s.serveCubeFiles(w, r, "v1FileContent") {
		return
	}
	id := r.PathValue("id")
	if !isULID(id) {
		writeV1Err(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	root := s.appDirFor(id)
	full, ok := safeJoin(root, r.URL.Query().Get("path"))
	if !ok || full == root {
		writeV1Err(w, http.StatusBadRequest, "invalid_request", "invalid path")
		return
	}
	rel, err := filepath.Rel(root, full)
	_, mnt := s.Loopback.Paths(id)
	file, err := openAppRead(mnt, rel, false)
	if err != nil {
		writeV1Err(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		writeV1Err(w, http.StatusInternalServerError, "internal", "unable to read file")
		return
	}
	if len(data) > maxFileBytes {
		writeV1Err(w, http.StatusBadRequest, "invalid_request", "file exceeds the 2 MiB read cap")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

// --- GET /v1/sandboxes/{id}/export ----------------------------------

func (s *Server) v1Export(w http.ResponseWriter, r *http.Request) {
	if s.serveCubeFiles(w, r, "v1Export") {
		return
	}
	id := r.PathValue("id")
	if !isULID(id) {
		writeV1Err(w, http.StatusNotFound, "not_found", "no workspace for that sandbox")
		return
	}
	_, mnt := s.Loopback.Paths(id)
	dir, err := openAppDirs(mnt, "")
	if err != nil {
		writeV1Err(w, http.StatusNotFound, "not_found", "no workspace for that sandbox")
		return
	}
	defer dir.close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	_ = walkAppFiles(dir.last(), "", true, func(path string, isDir bool, file *os.File) error {
		if isDir {
			return nil
		}
		fw, err := zw.Create(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, file)
		return err
	})
}
