package main

import (
	"encoding/json"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"io"
	"net/http"
	"path/filepath"
)

func (a *app) handlePrivateTaskHistory(w http.ResponseWriter, r *http.Request) {
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	allowed := a.workspaceQuiesced && !a.restartPending
	a.taskMu.Unlock()
	if !allowed {
		http.Error(w, "quiesced supervisor required", 409)
		return
	}
	if e := stopWorkspaceWriters(r.Context()); e != nil {
		http.Error(w, "cannot prove workspace quiescence", 503)
		return
	}
	root := filepath.Join(a.runtimeDir, "tasks")
	if r.Method == http.MethodPost {
		var req runtime.PrivateTaskHistoryRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || runtime.ValidatePrivateTaskIDs(req.TaskIDs) != nil {
			http.Error(w, "invalid task history IDs", 400)
			return
		}
		var tail any
		if dec.Decode(&tail) != io.EOF {
			http.Error(w, "invalid task history request", 400)
			return
		}
		data, e := runtime.ExportPrivateTaskHistory(r.Context(), root, req.TaskIDs)
		if e != nil {
			http.Error(w, "cannot export selected task history", 422)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Write(data)
		return
	}
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, runtime.MaxPrivateTaskHistoryBytes))
	if e != nil {
		http.Error(w, "task history archive limit", 413)
		return
	}
	if runtime.ValidatePrivateTaskHistoryArchive(data) != nil {
		http.Error(w, "invalid task history archive", 400)
		return
	}
	if e = runtime.InstallPrivateTaskHistory(root, data); e != nil {
		http.Error(w, "task history import conflicts or failed", 409)
		return
	}
	writeJSON(w, 200, map[string]bool{"imported": true})
}
