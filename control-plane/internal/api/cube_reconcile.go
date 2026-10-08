package api

import (
	"context"
	"errors"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtimepolicy"
)

type explicitRuntimeStartKey struct{}

const cubeRecoveryRequiredMessage = "Cube runtime requires operator recovery; binding retained"

// connectCube must be called under the sandbox lock by mutating callers.
// A control-plane response is not application readiness: verify the scoped
// supervisor before promoting durable state, including creating/error recovery.
func (s *Server) connectCube(ctx context.Context, id string, timeoutSeconds int) error {
	return s.connectCubeWithConfig(ctx, id, timeoutSeconds, true)
}

// Explicit manifest activation validates the manifest before applying pending
// config. Normal lifecycle calls retain their existing config synchronization.
func (s *Server) connectCubeWithConfig(ctx context.Context, id string, timeoutSeconds int, applyConfig bool) error {
	_, err := s.connectCubeReady(ctx, id, timeoutSeconds, applyConfig)
	return err
}

// The returned readiness belongs only to this locked lifecycle operation.
func (s *Server) connectCubeReady(ctx context.Context, id string, timeoutSeconds int, applyConfig bool) (*runtime.Status, error) {
	// Maintenance, file reads, task recovery and previews cannot allocate
	// compute for an upstream-managed account. The platform admits the start
	// through the authenticated explicit lifecycle endpoint first.
	if s.Store != nil {
		row, err := s.Store.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		explicit, _ := ctx.Value(explicitRuntimeStartKey{}).(bool)
		if row.Status != "running" && runtimepolicy.RequiresExplicitStart(row.ExternalUserID.String) && !explicit {
			return nil, errExplicitSandboxStart
		}
	}

	step := time.Now()
	mark := func(phase string) {
		if s.Log != nil {
			s.Log.Info("cube_start_phase", "sandbox_id", id, "phase", phase, "duration_ms", time.Since(step).Milliseconds())
		}
		step = time.Now()
	}
	defer func() { mark("finish") }()
	if s.Cube == nil {
		return nil, errors.New("Cube runtime disabled")
	}
	if active, err := s.Store.SandboxHasRunningTask(ctx, id); err != nil {
		return nil, err
	} else if active && timeoutSeconds < 86400+600 {
		timeoutSeconds = 86400 + 600
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 3600
	}
	b, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		return nil, err
	}
	var readyStatus *runtime.Status
	leaseDone := taskStage(ctx, "cube_connect_and_ready")
	err = s.withCubeCapacityRetry(ctx, id, func() error {
		_, e := s.Cube.ConnectAndCheck(ctx, b.RuntimeID, cube.ConnectRequest{TimeoutSeconds: timeoutSeconds}, func(checked context.Context) error {
			done := taskStage(checked, "supervisor_ready")
			defer done()
			ready, cancel := context.WithTimeout(checked, 15*time.Second)
			defer cancel()
			for {
				status, e := s.runtimeClientFor(id).Status(ready)
				if e == nil {
					readyStatus = status
					return nil
				}
				select {
				case <-ready.Done():
					return errors.New("Cube supervisor readiness failed")
				case <-time.After(100 * time.Millisecond):
				}
			}
		})
		return e
	})
	leaseDone()
	if err != nil {
		return nil, err
	}
	mark("provider_and_supervisor_ready")

	if applyConfig {
		done := taskStage(ctx, "config_sync")
		err := s.syncCubeAppConfig(ctx, id)
		done()
		if err != nil && !errors.Is(err, errCubeConfigBusy) {
			return nil, err
		}
	}
	mark("config_sync")
	sb, err := s.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// A config change can restart processes; don't reuse pre-change readiness.
	motionScoped := s.cubeEgress != nil && sb.AppID.Valid && s.cubeEgress.config.MotionStudioAppID == sb.AppID.String
	if applyConfig && (b.ConfigRevision != b.ConfigAppliedRevision || motionScoped) {
		readyStatus = nil
	}
	if sb.Status == "running" {
		if err := s.Store.BumpLastActive(ctx, id, time.Now().UTC()); err != nil {
			return nil, err
		}
	} else if err := s.Store.MarkRunningWoke(ctx, id, "", "", time.Now().UTC()); err != nil {
		return nil, err
	}
	egressDone := taskStage(ctx, "egress_ready")
	defer egressDone()
	if err = s.ensureCubeEgress(ctx, id); err != nil {
		return nil, err
	}
	return readyStatus, nil
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
			// List is only a candidate snapshot. A user may have stopped this
			// sandbox while we were waiting to acquire its lifecycle lock.
			current, err := s.Store.Get(bounded, sb.ID)
			if err != nil || current.RuntimeProvider != "cube" {
				return
			}
			sb = current
			b, err := s.Store.GetRuntimeBinding(bounded, sb.ID)
			if err != nil {
				return
			}
			remote, err := s.Cube.Get(bounded, b.RuntimeID)
			if err != nil {
				if errors.Is(err, cube.ErrRuntimeUnavailable) {
					// A known ID without a live task requires operator recovery.
					// Retain its binding and charged admission; never replace or
					// release it based on an unavailable runtime observation.
					s.cubePreviewLeases.Delete(sb.ID)
					_ = s.Store.MarkError(bounded, sb.ID, cubeRecoveryRequiredMessage)
					return
				}
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
			// Preserve explicit always-on/keepalive policies and open preview
			// streams. Cube's lease clock otherwise pauses these after an hour,
			// even though the Docker idle reaper correctly exempts them. A user
			// stop remains stopped unless an already accepted task needs recovery.
			retainLease := active || (sb.Status == "running" && (sb.IdlePolicy == "always_on" ||
				(sb.KeepaliveUntil.Valid && sb.KeepaliveUntil.Int64 > time.Now().Unix()) ||
				(s.Inflight != nil && s.Inflight.Active(sb.ID))))
			leaseSeconds := 3600
			if active {
				leaseSeconds = 86400 + 600
			}
			switch remote.State {
			case "paused":
				// Resuming is safe for an already accepted task; pausing it isn't. This
				// also recovers tasks that were frozen before a control-plane restart.
				if retainLease {
					mutation, done := context.WithTimeout(ctx, 130*time.Second)
					defer done()
					_ = s.connectCube(mutation, sb.ID, leaseSeconds)
					return
				}
				if sb.Status != "stopped" {
					s.stopCubeEgress(sb.ID)
					_ = s.Store.MarkStoppedAt(bounded, sb.ID, time.Now().UTC())
				}
			case "running":
				life := s.lifecycleView()
				if !retainLease && life.IdleReapEnabled && life.IdleThresholdSeconds > 0 &&
					!sb.LastActiveAt.IsZero() && time.Since(sb.LastActiveAt) >= time.Duration(life.IdleThresholdSeconds)*time.Second {
					// Cube guests do not enter the Docker reaper. Apply the same
					// operator idle setting here, including after a long task lease,
					// while holding the lifecycle lock and preserving remote errors.
					mutation, done := context.WithTimeout(ctx, 130*time.Second)
					defer done()
					if err := s.Cube.Pause(mutation, b.RuntimeID); err == nil {
						s.stopCubeEgress(sb.ID)
						s.cubePreviewLeases.Delete(sb.ID)
						_ = s.Store.MarkStoppedAt(mutation, sb.ID, time.Now().UTC())
					}
					return
				}
				if _, err := s.runtimeClientFor(sb.ID).Status(bounded); err != nil {
					return
				}
				if !active {
					if err := s.syncCubeAppConfig(bounded, sb.ID); err != nil {
						return
					}
				}
				// The authoritative lease, not the maintenance tick, determines
				// renewal. Avoid one cross-worker mutation per open stream on
				// every scan. Missing TTL keeps the conservative old behavior.
				if retainLease && (remote.EndAt == nil || time.Until(*remote.EndAt) < 10*time.Minute) {
					mutation, done := context.WithTimeout(ctx, 130*time.Second)
					defer done()
					if _, err := s.Cube.Connect(mutation, b.RuntimeID, cube.ConnectRequest{TimeoutSeconds: leaseSeconds}); err != nil {
						return
					}
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
	go s.runCubeTaskQueue(ctx)
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
