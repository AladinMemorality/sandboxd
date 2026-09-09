package api

import (
	"encoding/json"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/audit"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"net/http"
)

func (s *Server) v1TaskMessage(w http.ResponseWriter, r *http.Request) {
	id, taskID := r.PathValue("id"), r.PathValue("taskId")
	sb, err := s.Store.Get(r.Context(), id)
	if err != nil || !sb.AppID.Valid {
		writeV1Err(w, 404, "not_found", "no such project sandbox")
		return
	}
	if _, err := s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such project sandbox")
		return
	}
	task, err := s.Store.GetTask(r.Context(), taskID)
	if err != nil || task.SandboxID != id {
		writeV1Err(w, 404, "not_found", "no such task")
		return
	}
	var req runtime.TaskMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 160000)).Decode(&req); err != nil || !req.Valid() {
		writeV1Err(w, 400, "invalid_request", "a UUID message_id and a prompt of at most 80000 bytes are required")
		return
	}
	if err := s.runtimeClientFor(id).SendTaskMessage(r.Context(), taskID, req); err != nil {
		if errors.Is(err, runtime.ErrTaskInputUnavailable) {
			writeV1Err(w, 409, "task_input_unavailable", "The agent is finishing or this workspace needs an update before it can receive live messages. Try sending again when the build finishes.")
		} else {
			writeV1Err(w, 502, "sandbox_unavailable", "The message could not be delivered to the agent.")
		}
		return
	}
	s.auditAction(r, audit.Entry{Action: "task.message", Target: id, Detail: map[string]any{"task_id": taskID, "message_id": req.MessageID}})
	writeJSON(w, 202, map[string]string{"id": taskID, "message_id": req.MessageID, "status": "accepted"})
}
