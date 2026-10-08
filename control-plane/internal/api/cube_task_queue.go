package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/designskills"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func (s *Server) enqueueCubeTask(w http.ResponseWriter, r *http.Request, sb *store.Sandbox, agent string, req v1TaskSubmitReq) bool {
	if s.CubeTaskConcurrency <= 0 {
		return false
	}
	capacity, err := s.Store.CubeTaskQueueCapacity(r.Context(), sb.ID)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot resolve coding capacity")
		return true
	}
	if capacity == 0 {
		return false
	}
	if s.Secrets == nil {
		writeV1Err(w, 503, "runtime_unavailable", "encrypted coding queue is unavailable")
		return true
	}
	req.Agent = agent
	raw, err := json.Marshal(req)
	if err != nil {
		writeV1Err(w, 400, "invalid_request", "invalid coding request")
		return true
	}
	sealed, nonce, err := s.Secrets.Seal(raw)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot retain coding request")
		return true
	}
	id := newULID()
	task := &store.Task{TaskID: id, SandboxID: sb.ID, Agent: agent, Prompt: req.Prompt, TimeoutS: req.TimeoutS, ExternalUserID: sb.ExternalUserID, ExternalProjectID: sb.ExternalProjectID}
	if err = s.Store.EnqueueCubeTask(r.Context(), task, sealed, nonce); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeV1Err(w, 409, "task_in_progress", "a coding request is already pending for this project")
		} else if errors.Is(err, store.ErrTaskQueueFull) {
			writeV1Err(w, 429, "capacity_unavailable", "the coding queue is full; retry shortly")
		} else {
			writeV1Err(w, 503, "runtime_unavailable", "cannot retain coding request")
		}
		return true
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "sandbox_id": sb.ID, "status": "queued", "agent": agent, "events_url": fmt.Sprintf("/v1/sandboxes/%s/tasks/%s/events", sb.ID, id)})
	return true
}

func (s *Server) runCubeTaskQueue(ctx context.Context) {
	if s.CubeTaskConcurrency <= 0 {
		return
	}
	if err := s.Store.ResetPreparingCubeTasks(ctx); err != nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for i := 0; i < s.CubeTaskConcurrency; i++ {
			task, err := s.Store.ClaimCubeTask(ctx, newULID(), s.CubeTaskConcurrency)
			if err != nil || task == nil {
				break
			}
			go s.dispatchQueuedCubeTask(ctx, *task)
		}
	}
}
func (s *Server) dispatchQueuedCubeTask(parent context.Context, queued store.CubeQueuedTask) {
	// Preparation may be retried; no task can execute before the durable dispatch
	// checkpoint. A cancellation/restart invalidates the old claim token.
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	retry := func() {
		if queued.Attempts < 5 {
			_ = s.Store.RetryCubeTaskPreparation(context.Background(), queued)
		} else {
			result := failedResult(queued.TaskID, "sandbox_unavailable", "The sandbox could not be prepared after repeated attempts.")
			raw, _ := json.Marshal(result)
			_ = s.Store.FinishUndispatchedCubeTask(context.Background(), queued, string(raw))
		}
	}
	if s.Secrets == nil {
		retry()
		return
	}
	raw, err := s.Secrets.Open(queued.Ciphertext, queued.Nonce)
	if err != nil {
		retry()
		return
	}
	var req v1TaskSubmitReq
	if json.Unmarshal(raw, &req) != nil {
		retry()
		return
	}
	if s.Locks != nil {
		s.Locks.Lock(queued.SandboxID)
		defer s.Locks.Unlock(queued.SandboxID)
	}
	if err = s.connectCube(ctx, queued.SandboxID, int(watchWindowFor(req.TimeoutS).Seconds())+600); err != nil {
		retry()
		return
	}
	if err = designskills.Ensure(ctx, s.runtimeClientFor(queued.SandboxID)); err != nil {
		retry()
		return
	}
	s.cubeTaskSubmissions.Store(queued.TaskID, true)
	defer s.cubeTaskSubmissions.Delete(queued.TaskID)
	if err = s.Store.BeginCubeTaskDispatch(ctx, queued); err != nil {
		return
	}
	if err = s.prepareCubeModelScope(ctx, queued.SandboxID, queued.TaskID, &req); err != nil {
		raw, _ := json.Marshal(failedResult(queued.TaskID, "internal", "model relay scope could not be prepared"))
		_ = s.Store.FinishCubeTaskWithoutEvents(context.Background(), queued.TaskID, "failed", string(raw))
		return
	}
	err = s.runtimeClientFor(queued.SandboxID).StartTask(ctx, runtime.StartTaskRequest{TaskID: queued.TaskID, Prompt: designskills.Prompt() + req.Prompt, Agent: req.Agent, Model: req.Model, TimeoutS: req.TimeoutS, Continue: req.Continue, Env: req.Env})
	if errors.Is(err, runtime.ErrTaskInProgress) {
		raw, _ := json.Marshal(failedResult(queued.TaskID, "internal", "another task is already active"))
		_ = s.Store.FinishCubeTaskWithoutEvents(context.Background(), queued.TaskID, "failed", string(raw))
		return
	}
	// A network error here is ambiguous. Keep the durable running charge and
	// observe the guest; never dispatch the same request a second time.
	go s.watchTask(queued.SandboxID, queued.TaskID, req.TimeoutS)
}

// Waiting for a coding slot must not wake a guest or report its absent task as
// a failure. Once dispatched, stream the guest's normal event protocol.
func (s *Server) serveQueuedCubeTaskEvents(w http.ResponseWriter, r *http.Request) bool {
	id, taskID := r.PathValue("id"), r.PathValue("taskId")
	task, err := s.Store.GetTask(r.Context(), taskID)
	if err != nil || task.SandboxID != id {
		return false
	}
	terminalOnly, err := s.Store.CubeTaskTerminalOnly(r.Context(), taskID)
	if err != nil {
		writeV1Err(w, 503, "history_unavailable", "cannot resolve queued task history")
		return true
	}
	if task.Status != "queued" && !terminalOnly {
		return false
	}
	since := 0
	if n, e := strconv.Atoi(r.Header.Get("Last-Event-ID")); e == nil && n >= 0 && n < int(^uint(0)>>1) {
		since = n + 1
	}
	if n, e := strconv.Atoi(r.URL.Query().Get("since")); e == nil && n >= 0 {
		since = n
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for r.Context().Err() == nil {
		task, err = s.Store.GetTask(r.Context(), taskID)
		if err != nil {
			return true
		}
		if task.ResultJSON.Valid {
			if since == 0 {
				fmt.Fprintf(w, "id: 0\nevent: done\ndata: %s\n\n", task.ResultJSON.String)
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		if task.Status == "running" {
			body, e := s.runtimeClientFor(id).TaskEvents(r.Context(), taskID, since)
			if e == nil {
				_ = runtime.DecodeEvents(body, func(ev runtime.Event) bool {
					data := ev.Data
					if len(data) == 0 {
						data = json.RawMessage("{}")
					}
					fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, data)
					if flusher != nil {
						flusher.Flush()
					}
					return r.Context().Err() == nil
				})
				body.Close()
				return true
			}
		}
		fmt.Fprint(w, ": waiting for coding capacity\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return true
		case <-time.After(time.Second):
		}
	}
	return true
}
