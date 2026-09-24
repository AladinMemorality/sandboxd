package api

import (
	"context"
	"errors"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// connectCube must be called under the sandbox lock by mutating callers.
// A control-plane response is not application readiness: verify the scoped
// supervisor before promoting durable state, including creating/error recovery.
func (s *Server) connectCube(ctx context.Context, id string, timeoutSeconds int) error {
	if s.Cube == nil {
		return errors.New("Cube runtime disabled")
	}
	if active, err := s.Store.SandboxHasRunningTask(ctx, id); err != nil {
		return err
	} else if active && timeoutSeconds < 86400+600 {
		timeoutSeconds = 86400 + 600
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 3600
	}
	b, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		return err
	}
	if _, err = s.Cube.Connect(ctx, b.RuntimeID, cube.ConnectRequest{TimeoutSeconds: timeoutSeconds}); err != nil {
		return err
	}
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		if _, err = s.runtimeClientFor(id).Status(ready); err == nil {
			break
		}
		select {
		case <-ready.Done():
			return errors.New("Cube supervisor readiness failed")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := s.syncCubeAppConfig(ctx, id); err != nil && !errors.Is(err, errCubeConfigBusy) {
		return err
	}
	sb, err := s.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if sb.Status == "running" {
		if err := s.Store.BumpLastActive(ctx, id, time.Now().UTC()); err != nil {
			return err
		}
	} else if err := s.Store.MarkRunningWoke(ctx, id, "", "", time.Now().UTC()); err != nil {
		return err
	}
	return s.ensureCubeEgress(ctx, id)
}

// ReconcileCube reads authoritative remote state without creating/replacing a
// VM. Provider outages preserve recoverable bindings instead of destroying data.
func (s *Server) ReconcileCube(ctx context.Context) {
	if s.Cube == nil || s.Store == nil {
		return
	}
	rows, err := s.Store.List(ctx)
	if err != nil {
		return
	}
	for _, sb := range rows {
		if sb.RuntimeProvider != "cube" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if s.Locks != nil && !s.Locks.TryLock(sb.ID) {
			continue
		}
		func() {
			if s.Locks != nil {
				defer s.Locks.Unlock(sb.ID)
			}
			bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			b, err := s.Store.GetRuntimeBinding(bounded, sb.ID)
			if err != nil {
				return
			}
			remote, err := s.Cube.Get(bounded, b.RuntimeID)
			if err != nil {
				var apiErr *cube.APIError
				if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
					_ = s.Store.MarkError(bounded, sb.ID, "Cube runtime no longer exists; binding retained for investigation")
				}
				return
			}
			active, err := s.Store.SandboxHasRunningTask(bounded, sb.ID)
			if err != nil {
				return
			}
			switch remote.State {
			case "paused":
				// Resuming is safe for an already accepted task; pausing it isn't. This
				// also recovers tasks that were frozen before a control-plane restart.
				if active {
					_ = s.connectCube(bounded, sb.ID, 86400+600)
					return
				}
				if sb.Status != "stopped" {
					s.stopCubeEgress(sb.ID)
					_ = s.Store.MarkStoppedAt(bounded, sb.ID, time.Now().UTC())
				}
			case "running":
				if _, err := s.runtimeClientFor(sb.ID).Status(bounded); err != nil {
					return
				}
				if !active {
					if err := s.syncCubeAppConfig(bounded, sb.ID); err != nil {
						return
					}
				}
				if active {
					_, _ = s.Cube.Connect(bounded, b.RuntimeID, cube.ConnectRequest{TimeoutSeconds: 86400 + 600})
				}
				if sb.Status != "running" {
					_ = s.Store.MarkRunningWoke(bounded, sb.ID, "", "", time.Now().UTC())
				}
				_ = s.ensureCubeEgress(bounded, sb.ID)
			}
		}()
	}
}

// RunCubeMaintenance reconciles remote state and durable task results after
// transient failures. The context belongs to sandboxd's process lifecycle.
func (s *Server) RunCubeMaintenance(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ReconcileCube(ctx)
			s.reconcileCubeTasks(ctx)
		}
	}
}
