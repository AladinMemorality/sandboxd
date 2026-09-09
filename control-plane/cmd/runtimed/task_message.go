package main

import (
	"encoding/json"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"net/http"
)

func (a *app) handleTaskMessage(w http.ResponseWriter, r *http.Request) {
	var req runtime.TaskMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 160000)).Decode(&req); err != nil || !req.Valid() {
		writeJSON(w, 400, map[string]string{"error": "a UUID message_id and a prompt of at most 80000 bytes are required"})
		return
	}
	a.taskMu.Lock()
	t := a.task
	a.taskMu.Unlock()
	if t == nil || t.id != r.PathValue("id") {
		writeJSON(w, 404, map[string]string{"error": "no such active task"})
		return
	}
	if t.input == nil {
		writeJSON(w, 409, map[string]string{"error": "this agent does not support live messages"})
		return
	}
	added, err := t.input.send(req)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	if added {
		t.emit("input", map[string]any{"message_id": req.MessageID, "status": "accepted", "text": req.Prompt})
	}
	writeJSON(w, 202, map[string]string{"id": t.id, "message_id": req.MessageID, "status": "accepted"})
}
