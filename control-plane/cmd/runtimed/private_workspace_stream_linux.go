package main

import (
	"io"
	"net/http"
	"os"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func (a *app) handlePrivateWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	allowed := a.workspaceQuiesced && !a.restartPending && (r.Method == http.MethodGet || a.requestRestart != nil)
	a.taskMu.Unlock()
	if !allowed {
		http.Error(w, "quiesced restart-capable supervisor required", 409)
		return
	}
	if err := stopWorkspaceWriters(r.Context()); err != nil {
		http.Error(w, "cannot prove workspace quiescence", 503)
		return
	}
	// Spool only within guest runtime state, outside the app tree being replaced.
	f, err := os.CreateTemp(a.runtimeDir, ".private-workspace-v2-")
	if err != nil {
		scopedError(w, err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if r.Method == http.MethodGet {
		if err = runtime.ExportPrivateWorkspaceFile(r.Context(), a.appDir, "/home/sandbox", f); err != nil {
			http.Error(w, "private workspace export rejected", 422)
			return
		}
		if err = f.Sync(); err != nil {
			scopedError(w, err)
			return
		}
		size, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			scopedError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		http.ServeContent(w, r, "workspace.zip", time.Time{}, io.NewSectionReader(f, 0, size))
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, runtime.MaxPrivateWorkspaceStreamBytes))
	if err != nil {
		http.Error(w, "private workspace upload limit", 413)
		return
	}
	if err = f.Sync(); err != nil {
		scopedError(w, err)
		return
	}
	if err = runtime.ValidatePrivateWorkspaceFileInterpreters(f, n); err != nil {
		http.Error(w, "invalid private workspace archive", 422)
		return
	}
	if err = runtime.InstallPrivateWorkspaceFile(r.Context(), a.appDir, f, n); err != nil {
		http.Error(w, "private workspace import rejected", 422)
		return
	}
	a.taskMu.Lock()
	a.restartPending = true
	a.taskMu.Unlock()
	writeJSON(w, 200, map[string]bool{"imported": true, "restarting": true})
	if flush, ok := w.(http.Flusher); ok {
		flush.Flush()
	}
	go func() { time.Sleep(100 * time.Millisecond); a.requestRestart() }()
}
