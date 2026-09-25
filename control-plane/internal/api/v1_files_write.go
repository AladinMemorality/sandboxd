package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/audit"
)

// PUT /v1/sandboxes/{id}/files?path=<rel> atomically replaces an app-relative
// file with opaque request bytes. Filesystem operations use retained directory
// descriptors; tenant-controlled symlinks are never followed. Files receive the
// workspace owner and mode 0644. The request limit is 25 MiB.

const (
	// maxPutFileBytes — per-request body cap. Mirrors uploads.go.
	maxPutFileBytes = 25 << 20
)

// reservedPathPrefixes are subtrees of the workspace mount that the
// platform owns. Writes here are refused even with a valid token.
var reservedPathPrefixes = []string{".runtimed/", ".runtimed", "lost+found/", "lost+found"}

// resolveWritePath validates a caller-supplied relative path against
// the workspace mount root and returns the absolute on-disk path.
//
// Rules (in order):
//  1. path is non-empty and does not contain a NUL byte.
//  2. path is not absolute.
//  3. After Clean, no segment is `..` (would escape) and no segment is
//     empty (would denote a directory write).
//  4. The cleaned path does not target a reserved subtree.
//  5. The resolved on-disk path stays under <mnt>/.
func resolveWritePath(mnt, raw string) (string, string, error) {
	if raw == "" {
		return "", "", errors.New("path is required")
	}
	if strings.ContainsRune(raw, 0) {
		return "", "", errors.New("invalid path: NUL byte")
	}
	if filepath.IsAbs(raw) {
		return "", "", errors.New("path must be relative to the workspace root")
	}
	// Trailing slash signals directory intent — check before Clean,
	// which would strip it.
	if strings.HasSuffix(raw, "/") {
		return "", "", errors.New("path must name a file, not a directory")
	}
	clean := filepath.Clean(raw)
	// Reject any traversal segment. filepath.Clean reduces "a/../b" to
	// "b" but leaves a leading ".." in place.
	for _, seg := range strings.Split(clean, string(os.PathSeparator)) {
		if seg == ".." {
			return "", "", errors.New("path traversal (..) not allowed")
		}
	}
	if clean == "." || clean == "/" {
		return "", "", errors.New("path must name a file, not the root")
	}
	if strings.HasSuffix(clean, "/") {
		return "", "", errors.New("path must name a file, not a directory")
	}
	for _, p := range reservedPathPrefixes {
		if clean == strings.TrimSuffix(p, "/") ||
			strings.HasPrefix(clean, strings.TrimSuffix(p, "/")+"/") {
			return "", "", errors.New("path is in a reserved subtree (" +
				strings.TrimSuffix(p, "/") + ")")
		}
	}
	full := filepath.Join(mnt, clean)
	// Defence-in-depth: re-check the final prefix after Join.
	if full != mnt && !strings.HasPrefix(full, mnt+string(os.PathSeparator)) {
		return "", "", errors.New("resolved path escapes the workspace mount")
	}
	return full, clean, nil
}

// v1PutFile is the handler for PUT /v1/sandboxes/{id}/files.
func (s *Server) v1PutFile(w http.ResponseWriter, r *http.Request) {
	if s.serveCubeFiles(w, r, "v1PutFile") {
		return
	}
	id := r.PathValue("id")
	if !isULID(id) {
		writeV1Err(w, http.StatusBadRequest, "invalid_request", "invalid sandbox id")
		return
	}
	_, mnt := s.Loopback.Paths(id)
	if info, err := os.Stat(mnt); err != nil || !info.IsDir() {
		writeV1Err(w, http.StatusNotFound, "not_found", "no workspace for that sandbox")
		return
	}

	// Paths are relative to the APP dir (workspace/app), matching GET /files and
	// /files/content. Writing relative to the workspace root instead left console
	// editor saves invisible to reads (they landed a directory above the app), so
	// the editor looked read-only. appDirFor is under mnt, so the chown-up loop
	// below (bounded by mnt) still fixes ownership of any dirs it creates.
	_, rel, err := resolveWritePath(s.appDirFor(id), r.URL.Query().Get("path"))
	if err != nil {
		writeV1Err(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Hard size gate before reading the body.
	if r.ContentLength > maxPutFileBytes {
		writeV1Err(w, http.StatusRequestEntityTooLarge, "invalid_request",
			"file exceeds the 25 MiB limit")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPutFileBytes)

	written, err := writeAppFile(mnt, rel, r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			writeV1Err(w, http.StatusRequestEntityTooLarge, "invalid_request", "file exceeds the 25 MiB limit")
		case errors.Is(err, errUnsafeFilePath):
			writeV1Err(w, http.StatusBadRequest, "invalid_request", "file path contains a link, special file, or changed directory")
		default:
			writeV1Err(w, http.StatusInternalServerError, "internal", "unable to write file")
		}
		return
	}

	s.auditAction(r, audit.Entry{
		Action: "file.put", Target: id,
		Detail: map[string]any{"path": rel, "size": written},
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "size": written})
}
