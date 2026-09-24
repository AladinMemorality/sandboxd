package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func validCubeTaskResult(result *runtime.TaskResult, id string) bool {
	if result == nil || result.ID != id {
		return false
	}
	return result.Status == runtime.TaskSucceeded || result.Status == runtime.TaskFailed || result.Status == runtime.TaskCancelled
}

// recoverCubeTask never fabricates failure from a transport outage: the VM may
// still be working. Only an authenticated guest result or explicit absence can
// finalize a row. No shared host file or Docker operation is used.
func (s *Server) recoverCubeTask(ctx context.Context, t *store.Task) {
	if _, active := s.cubeTaskWatches.Load(t.TaskID); active {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := s.runtimeClientFor(t.SandboxID)
	result, err := client.TaskResult(bounded, t.TaskID)
	if err == nil && validCubeTaskResult(result, t.TaskID) {
		s.finishWatchedTask(t.SandboxID, t.TaskID, result)
		return
	}
	var responseErr *runtime.ResponseError
	if err != nil && (!errors.As(err, &responseErr) || (responseErr.StatusCode != 404 && responseErr.StatusCode != 409)) {
		return
	}
	status, err := client.Status(bounded)
	if err != nil {
		return
	}
	if status.ActiveTask != nil && status.ActiveTask.ID == t.TaskID {
		go s.watchTask(t.SandboxID, t.TaskID, t.TimeoutS)
		return
	}
	// Allow a just-submitted task a short startup/result-write grace period.
	if responseErr != nil && responseErr.StatusCode == 404 && time.Since(t.CreatedAt) > 5*time.Second {
		s.finishWatchedTask(t.SandboxID, t.TaskID, failedResult(t.TaskID, "sandbox_unavailable", "guest reports neither the task nor a completed result"))
	}
}

func (s *Server) reconcileCubeTasks(ctx context.Context) {
	tasks, err := s.Store.ListRunningTasks(ctx)
	if err != nil {
		return
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}
		if remote, err := s.Store.IsCube(ctx, t.SandboxID); err == nil && remote {
			s.recoverCubeTask(ctx, t)
		}
	}
}

func (s *Server) watchCubeTask(sandboxID, taskID string, window time.Duration) {
	if _, loaded := s.cubeTaskWatches.LoadOrStore(taskID, true); loaded {
		return
	}
	defer s.cubeTaskWatches.Delete(taskID)
	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()
	rc := s.runtimeClientFor(sandboxID)
	since := 0
	for ctx.Err() == nil {
		body, err := rc.TaskEvents(ctx, taskID, since)
		var result *runtime.TaskResult
		if err == nil {
			_ = runtime.DecodeEvents(body, func(ev runtime.Event) bool {
				since = ev.ID + 1
				if ev.Type == runtime.EventDone {
					var candidate runtime.TaskResult
					if json.Unmarshal(ev.Data, &candidate) == nil && validCubeTaskResult(&candidate, taskID) {
						result = &candidate
					}
					return false
				}
				return ctx.Err() == nil
			})
			_ = body.Close()
		}
		if result == nil {
			if got, e := rc.TaskResult(ctx, taskID); e == nil && validCubeTaskResult(got, taskID) {
				result = got
			}
		}
		if result != nil {
			s.finishWatchedTask(sandboxID, taskID, result)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// prepareCubeTaskRPC resumes a frozen guest before control operations. Durable
// result/list reads don't call this: those remain available without waking a VM.
func (s *Server) prepareCubeTaskRPC(ctx context.Context, id string) error {
	remote, err := s.Store.IsCube(ctx, id)
	if err != nil || !remote {
		return err
	}
	if s.Locks != nil {
		s.Locks.Lock(id)
		defer s.Locks.Unlock(id)
	}
	sb, err := s.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if sb.Status == "running" {
		if _, err := s.runtimeClientFor(id).Status(probe); err == nil {
			return nil
		}
	}
	return s.connectCube(ctx, id, 3600)
}
