package main

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *app) workspaceFence(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		history := r.URL.Path == "/export/private-task-history" || r.URL.Path == "/import/private-task-history" || r.URL.Path == "/export/private-home" || r.URL.Path == "/import/private-home" || r.URL.Path == "/export/private-home-v2" || r.URL.Path == "/import/private-home-v2"
		streaming := r.URL.Path == "/export/private-workspace-v2" || r.URL.Path == "/import/private-workspace-v2"
		control := history || streaming || strings.HasPrefix(r.URL.Path, "/workspace/") || (r.URL.Path == "/import/private-workspace" || r.URL.Path == "/import/git-workspace") || r.URL.Path == "/export/private-workspace" || r.URL.Path == "/import/source"
		if !control && r.Method != http.MethodGet && r.Method != http.MethodHead {
			a.workspaceMu.RLock()
			defer a.workspaceMu.RUnlock()
		}
		a.taskMu.Lock()
		paused := a.workspaceQuiesced
		a.taskMu.Unlock()
		if paused && !history && !streaming && r.Method != "GET" && r.URL.Path != "/workspace/resume" && r.URL.Path != "/workspace/quiesce" && r.URL.Path != "/import/private-workspace" && r.URL.Path != "/import/git-workspace" && r.URL.Path != "/config" {
			http.Error(w, "workspace is quiesced for migration", 409)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *app) handleWorkspaceQuiesce(w http.ResponseWriter, r *http.Request) {
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
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
	if err := os.MkdirAll(a.runtimeDir, 0700); err != nil {
		scopedError(w, err)
		return
	}
	f, err := os.OpenFile(filepath.Join(a.runtimeDir, "workspace-quiesced"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		scopedError(w, err)
		return
	}
	err = f.Sync()
	f.Close()
	if err == nil {
		dir, e := os.Open(a.runtimeDir)
		if e != nil {
			err = e
		} else {
			err = dir.Sync()
			dir.Close()
		}
	}
	if err != nil {
		scopedError(w, err)
		return
	}
	a.workspaceQuiesced = true
	if a.web != nil {
		a.web.suspend()
	}
	for _, p := range a.workers {
		p.suspend()
	}
	if err := stopWorkspaceWriters(r.Context()); err != nil {
		http.Error(w, "cannot prove workspace quiescence", 503)
		return
	}
	writeJSON(w, 200, map[string]bool{"quiesced": true})
}
func (a *app) handleWorkspaceResume(w http.ResponseWriter, r *http.Request) {
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	if a.restartPending {
		http.Error(w, "supervisor restarting", 409)
		return
	}
	if !a.workspaceQuiesced {
		writeJSON(w, 200, map[string]bool{"resumed": true})
		return
	}
	if err := os.Remove(filepath.Join(a.runtimeDir, "workspace-quiesced")); err != nil && !os.IsNotExist(err) {
		scopedError(w, err)
		return
	}
	if dir, e := os.Open(a.runtimeDir); e == nil {
		e = dir.Sync()
		dir.Close()
		if e != nil {
			scopedError(w, e)
			return
		}
	} else {
		scopedError(w, e)
		return
	}
	a.workspaceQuiesced = false
	a.restartPending = false
	if a.web != nil {
		a.web.resume()
	}
	for _, p := range a.workers {
		p.resume()
	}
	writeJSON(w, 200, map[string]bool{"resumed": true})
}
func (a *app) handlePrivateWorkspaceExport(w http.ResponseWriter, r *http.Request) {
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	quiesced := a.workspaceQuiesced
	a.taskMu.Unlock()
	if !quiesced {
		http.Error(w, "quiesce workspace before export", 409)
		return
	}
	if err := stopWorkspaceWriters(r.Context()); err != nil {
		http.Error(w, "cannot prove workspace quiescence", 503)
		return
	}
	data, err := runtime.ExportPrivateWorkspaceOwnerContext(r.Context(), a.appDir, "/home/sandbox")
	if err != nil {
		scopedError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	digest, _ := runtime.PrivateWorkspaceDigest(data)
	w.Header().Set("X-Workspace-SHA256", digest)
	w.Write(data)
}
func (a *app) handlePrivateWorkspaceImport(w http.ResponseWriter, r *http.Request) {
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	allowed := a.workspaceQuiesced && a.requestRestart != nil && !a.restartPending
	a.taskMu.Unlock()
	if !allowed {
		http.Error(w, "quiesced restart-capable supervisor required", 409)
		return
	}
	if err := stopWorkspaceWriters(r.Context()); err != nil {
		http.Error(w, "cannot prove workspace quiescence", 503)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, runtime.MaxPrivateWorkspaceBytes))
	if err != nil {
		http.Error(w, "workspace archive exceeds limit", 413)
		return
	}
	if err = runtime.ValidatePrivateWorkspaceInterpreters(data); err != nil {
		http.Error(w, "invalid private workspace archive", 400)
		return
	}
	var prepare func(string) error
	if r.URL.Path == "/import/git-workspace" {
		prepare = func(staged string) error {
			for _, name := range []string{"node_modules", ".venv"} {
				if e := os.RemoveAll(filepath.Join(staged, name)); e != nil {
					return e
				}
			}
			if e := prepareSourceDependencies(r.Context(), staged, a.appDir); e != nil {
				return e
			}
			return stopWorkspaceWriters(r.Context())
		}
	}
	if err = runtime.InstallPrivateWorkspacePrepared(a.appDir, data, prepare); err != nil {
		scopedError(w, err)
		return
	}
	a.taskMu.Lock()
	a.restartPending = true
	a.taskMu.Unlock()
	// Quiescence marker outside the imported tree survives reexec, so user code
	// cannot write until the migration engine validates and explicitly resumes.
	digest, _ := runtime.PrivateWorkspaceDigest(data)
	writeJSON(w, 200, map[string]any{"imported": true, "restarting": true, "sha256": digest})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() { time.Sleep(100 * time.Millisecond); a.requestRestart() }()
}
