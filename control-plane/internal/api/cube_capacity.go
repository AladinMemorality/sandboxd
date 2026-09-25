package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// One nonwaiting reclaimer per process. It never queues behind another caller
// or a busy sandbox, and never holds two sandbox locks that it waited to obtain.
var cubeCapacityReclaim sync.Mutex

func (s *Server) withCubeCapacityRetry(ctx context.Context, requested string, operation func() error) error {
	err := operation()
	if !errors.Is(err, cube.ErrCapacityUnavailable) || errors.Is(err, cube.ErrCreationBusy) || ctx.Err() != nil {
		return err
	}
	if !cubeCapacityReclaim.TryLock() {
		return err
	}
	defer cubeCapacityReclaim.Unlock()
	if !s.reclaimIdleCube(ctx, requested) || ctx.Err() != nil {
		return err
	}
	// Only explicit pre-mutation refusal reaches here. Ambiguous Create/Pause or
	// provider errors never cause another mutation or another victim.
	return operation()
}

func idleCubeCandidate(sb *store.Sandbox, requested string, cutoff, now time.Time) bool {
	return sb != nil && sb.ID != requested && sb.RuntimeProvider == "cube" && sb.Status == "running" &&
		sb.IdlePolicy == "sleep" && !sb.LastActiveAt.IsZero() && sb.LastActiveAt.Before(cutoff) &&
		!(sb.KeepaliveUntil.Valid && sb.KeepaliveUntil.Int64 > now.Unix())
}

func (s *Server) reclaimIdleCube(ctx context.Context, requested string) bool {
	if s.Cube == nil || s.Store == nil || s.Locks == nil || s.Inflight == nil {
		return false
	}
	life := s.lifecycleView()
	if !life.IdleReapEnabled || life.IdleThresholdSeconds <= 0 {
		return false
	}
	// Recovery/migration operates offline. Refuse reclamation if an incomplete
	// journal is unexpectedly present; never guess which retained copy is safe.
	if pending, e := s.Store.HasIncompleteCubeRecoveries(ctx); e != nil || pending {
		return false
	}
	if pending, e := s.Store.HasIncompleteRuntimeMigrations(ctx); e != nil || pending {
		return false
	}
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(life.IdleThresholdSeconds) * time.Second)
	candidates, e := s.Store.ListIdleCandidates(ctx, cutoff)
	if e != nil {
		return false
	}
	for _, candidate := range candidates {
		if !idleCubeCandidate(candidate, requested, cutoff, now) || !s.Locks.TryLock(candidate.ID) {
			continue
		}
		eligible, paused := func() (bool, bool) {
			defer s.Locks.Unlock(candidate.ID)
			bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			current, e := s.Store.Get(bounded, candidate.ID)
			if e != nil || !idleCubeCandidate(current, requested, cutoff, time.Now().UTC()) || s.Inflight.Active(candidate.ID) {
				return false, false
			}
			if task, e := s.Store.SandboxHasRunningTask(bounded, candidate.ID); e != nil || task {
				return false, false
			}
			binding, e := s.Store.GetRuntimeBinding(bounded, candidate.ID)
			if e != nil {
				return false, false
			}
			reservation, e := s.Store.AdmissionLookup(bounded, binding.RuntimeID)
			if e != nil {
				return false, false
			}
			remote, e := s.Cube.Get(bounded, binding.RuntimeID)
			if e != nil || !validCubeAdmissionIdentity(current, binding, reservation, remote) || (remote.State != "running" && remote.State != "paused") {
				return false, false
			}
			// Preview registration and task/lifecycle mutation take this same lock.
			// An incoming request that registers first is protected; one that arrives
			// later waits until the confirmed pause then follows ordinary admission.
			if s.Inflight.Active(candidate.ID) {
				return false, false
			}
			if remote.State == "running" {
				if e = s.Cube.Pause(bounded, binding.RuntimeID); e != nil {
					return true, false
				}
			}
			released, e := s.Store.AdmissionLookup(bounded, binding.RuntimeID)
			if e != nil || released.State != "released" || released.Charged != 0 {
				return true, false
			}
			s.stopCubeEgress(candidate.ID)
			s.cubePreviewLeases.Delete(candidate.ID)
			if e = s.Store.MarkStoppedAt(bounded, candidate.ID, time.Now().UTC()); e != nil {
				return true, false
			}
			return true, true
		}()
		if eligible {
			return paused
		}
	}
	return false
}
