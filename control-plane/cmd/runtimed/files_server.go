package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"golang.org/x/sys/unix"
)

func scopedError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	message := "workspace operation failed"
	switch {
	case errors.Is(err, errScopedLimit):
		code = http.StatusRequestEntityTooLarge
		message = "workspace limit exceeded"
	case errors.Is(err, errScopedPath), errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
		code = http.StatusBadRequest
		message = "invalid workspace path"
	case errors.Is(err, os.ErrNotExist):
		code = http.StatusNotFound
		message = "no such file or directory"
	}
	writeJSON(w, code, map[string]string{"error": message})
}
func (a *app) handleFileList(w http.ResponseWriter, r *http.Request) {
	out, err := scopedList(r.Context(), a.appDir, r.URL.Query().Get("path"), r.URL.Query().Get("recursive") == "true")
	if err != nil {
		scopedError(w, err)
		return
	}
	data, err := json.Marshal(out)
	if err != nil {
		scopedError(w, err)
		return
	}
	if len(data) > runtime.MaxFileReadBytes {
		scopedError(w, errScopedLimit)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
func (a *app) handleFileRead(w http.ResponseWriter, r *http.Request) {
	data, err := scopedRead(a.appDir, r.URL.Query().Get("path"), runtime.MaxFileReadBytes, true)
	if err != nil {
		scopedError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}
func (a *app) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > runtime.MaxFileWriteBytes {
		scopedError(w, errScopedLimit)
		return
	}
	body := http.MaxBytesReader(w, r.Body, runtime.MaxFileWriteBytes)
	data, err := io.ReadAll(body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			scopedError(w, errScopedLimit)
		} else {
			http.Error(w, "invalid request body", 400)
		}
		return
	}
	name := r.URL.Query().Get("path")
	if err = scopedWrite(a.appDir, name, data); err != nil {
		scopedError(w, err)
		return
	}
	writeJSON(w, 200, runtime.FileWrite{Path: name, Size: int64(len(data))})
}
func (a *app) handleFileExport(w http.ResponseWriter, r *http.Request) {
	data, err := scopedExport(r.Context(), a.appDir)
	if err != nil {
		scopedError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Write(data)
}

var scopedIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (a *app) handleTaskResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !scopedIdentifier.MatchString(id) {
		scopedError(w, errScopedPath)
		return
	}
	data, err := scopedRead(a.runtimeDir, "tasks/"+id+"/result.json", runtime.MaxFileReadBytes, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if active := a.activeTaskRef(); active != nil && active.ID == id {
				http.Error(w, "task is still running", 409)
				return
			}
		}
		scopedError(w, err)
		return
	}
	var out runtime.TaskResult
	if json.Unmarshal(data, &out) != nil || out.ID != id || (out.Status != runtime.TaskSucceeded && out.Status != runtime.TaskFailed && out.Status != runtime.TaskCancelled) {
		http.Error(w, "task result is not finalized", 409)
		return
	}
	writeJSON(w, 200, &out)
}
func (a *app) handleProcessLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !scopedIdentifier.MatchString(name) || len(name) > 64 {
		scopedError(w, errScopedPath)
		return
	}
	known := a.web != nil && a.web.name == name
	for _, p := range a.workers {
		if p.name == name {
			known = true
		}
	}
	if !known {
		scopedError(w, os.ErrNotExist)
		return
	}
	tail := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("tail")); err == nil && n > 0 {
		tail = n
	}
	if tail > 1000 {
		tail = 1000
	}
	dir, err := openScopedRoot(a.runtimeDir)
	if err != nil {
		scopedError(w, err)
		return
	}
	defer dir.Close()
	f, err := openChild(dir, name+".log", false)
	if err != nil {
		scopedError(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		scopedError(w, err)
		return
	}
	if !info.Mode().IsRegular() {
		scopedError(w, errScopedPath)
		return
	}
	start := int64(0)
	if info.Size() > runtime.MaxProcessLogBytes {
		start = info.Size() - runtime.MaxProcessLogBytes
	}
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		scopedError(w, err)
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, runtime.MaxProcessLogBytes))
	if err != nil {
		scopedError(w, err)
		return
	}
	lines := []string{}
	text := strings.TrimRight(string(data), "\n")
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	writeJSON(w, 200, runtime.ProcessLog{Process: name, Lines: lines})
}
