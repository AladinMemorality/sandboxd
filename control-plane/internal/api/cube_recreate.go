package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Cube applies a complete config revision by restarting its supervisor in the
// same VM. Preserve the workspace and stable ID; an already applied revision
// is an idempotent success, avoiding a second restart after config CRUD.
func (s *Server) cubeRecreateSandbox(w http.ResponseWriter, r *http.Request, id string) bool {
	sb, err := s.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot resolve runtime")
		return true
	}
	if sb.RuntimeProvider != "cube" {
		return false
	}
	if s.Locks != nil {
		s.Locks.Lock(id)
		defer s.Locks.Unlock(id)
	}
	active, err := s.Store.SandboxHasRunningTask(r.Context(), id)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot resolve active tasks")
		return true
	}
	if active {
		writeV1Err(w, 409, "task_in_progress", "a task is in progress; apply config after it finishes")
		return true
	}
	bounded, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if s.Cube == nil {
		writeV1Err(w, 503, "runtime_unavailable", "Cube runtime is not configured")
		return true
	}
	if err = s.connectCube(bounded, id, 3600); err != nil {
		writeV1Err(w, 502, "runtime_unavailable", "Cube config application failed")
		return true
	}
	status, err := s.runtimeClientFor(id).Status(bounded)
	if err != nil {
		writeV1Err(w, 502, "runtime_unavailable", "Cube supervisor unavailable")
		return true
	}
	if status.ActiveTask != nil {
		writeV1Err(w, 409, "task_in_progress", "a task is in progress; apply config after it finishes")
		return true
	}
	if err = s.syncCubeAppConfig(bounded, id); err != nil {
		if errors.Is(err, errCubeConfigBusy) {
			writeV1Err(w, 409, "task_in_progress", "a task is in progress")
		} else {
			writeV1Err(w, 502, "runtime_unavailable", "Cube config acknowledgement pending")
		}
		return true
	}
	sb, err = s.Store.Get(bounded, id)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot read sandbox")
		return true
	}
	writeJSON(w, 200, s.v1SandboxFromRow(r, sb))
	return true
}
