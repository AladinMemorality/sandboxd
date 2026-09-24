// Package migration performs offline, same-owner provider transitions. It must
// be used under maintenance.Acquire(database,true), never from a tenant route.
package migration

import (
	"context"
	"errors"
	"fmt"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type Backend interface {
	ValidateRollback(context.Context, *store.RuntimeMigration) error
	PrepareRollback(context.Context, *store.RuntimeMigration) error
	StopSource(context.Context, *store.RuntimeMigration) error
	ArchiveSource(context.Context, *store.RuntimeMigration) (string, error)
	StageTarget(context.Context, *store.RuntimeMigration) error
	ImportTarget(context.Context, *store.RuntimeMigration) error
	VerifyTarget(context.Context, *store.RuntimeMigration) error
	ReadyTarget(context.Context, *store.RuntimeMigration) error
	ArchiveTarget(context.Context, *store.RuntimeMigration) (string, error)
	RestoreSource(context.Context, *store.RuntimeMigration) error
	PauseTarget(context.Context, *store.RuntimeMigration) error
}

type Engine struct {
	Store   *store.Store
	Backend Backend
	// AfterPhase is a test-only crash injection point after a durable transition.
	AfterPhase  func(string) error
	BeforePhase func() error
}

func (e *Engine) advance(ctx context.Context, m *store.RuntimeMigration, to, checksum string) error {
	if err := e.Store.AdvanceRuntimeMigration(ctx, m.SandboxID, m.Phase, to, checksum); err != nil {
		return err
	}
	if e.AfterPhase != nil {
		return e.AfterPhase(to)
	}
	return nil
}

// Run resumes from the last acknowledged phase. Ambiguous remote creation is
// intentionally not retried: an operator first adopts its existing identity.
func (e *Engine) Run(ctx context.Context, id string) error {
	for {
		if e.BeforePhase != nil {
			if err := e.BeforePhase(); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := e.Store.GetRuntimeMigration(ctx, id)
		if err != nil {
			return err
		}
		switch m.Phase {
		case "planned":
			if err = e.Backend.StopSource(ctx, m); err == nil {
				err = e.advance(ctx, m, "quiesced", "")
			}
		case "quiesced":
			var digest string
			digest, err = e.Backend.ArchiveSource(ctx, m)
			if err == nil {
				err = e.advance(ctx, m, "archived", digest)
			}
		case "archived":
			err = e.Backend.StageTarget(ctx, m)
			if err == nil && e.AfterPhase != nil {
				err = e.AfterPhase("staged")
			}
		case "staging":
			return errors.New("Cube creation acknowledgement is uncertain; adopt the existing tagged runtime before resuming; creation will not be retried")
		case "staged":
			if err = e.Backend.ImportTarget(ctx, m); err == nil {
				err = e.advance(ctx, m, "imported", "")
			}
		case "imported":
			if err = e.Backend.VerifyTarget(ctx, m); err == nil {
				err = e.advance(ctx, m, "verified", "")
			}
		case "verified":
			if err = e.Backend.ReadyTarget(ctx, m); err == nil {
				err = e.Backend.PauseTarget(ctx, m)
			}
			if err == nil {
				err = e.Store.CommitRuntimeMigration(ctx, id)
			}
			if err == nil && e.AfterPhase != nil {
				err = e.AfterPhase("complete")
			}
		case "complete":
			return nil
		default:
			return fmt.Errorf("migration cannot resume from phase %s", m.Phase)
		}
		if err != nil {
			return fmt.Errorf("migration phase %s: %w", m.Phase, err)
		}
	}
}

func (e *Engine) RollbackEligible(ctx context.Context, m *store.RuntimeMigration) error {
	active, err := e.Store.SandboxHasRunningTask(ctx, m.SandboxID)
	if err != nil {
		return err
	}
	if active {
		return errors.New("active task prevents rollback")
	}
	return e.Backend.ValidateRollback(ctx, m)
}

// Rollback exports current target writes before touching the retained source.
// Every destructive local replacement has a private, durable archive first.
func (e *Engine) Rollback(ctx context.Context, id string) error {
	for {
		if e.BeforePhase != nil {
			if err := e.BeforePhase(); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := e.Store.GetRuntimeMigration(ctx, id)
		if err != nil {
			return err
		}
		if m.Phase != "complete" && m.Phase != "rolled_back" {
			fingerprint, checkErr := e.Store.RuntimeConfigFingerprint(ctx, m.Source.AppID.String)
			if checkErr != nil {
				return checkErr
			}
			if fingerprint != m.RollbackConfigFingerprint {
				return errors.New("runtime config changed during rollback; refusing stale recovery")
			}
		}
		switch m.Phase {
		case "complete":
			if err = e.RollbackEligible(ctx, m); err == nil {
				err = e.advance(ctx, m, "rollback_started", "")
			}
		case "rollback_started":
			var digest string
			digest, err = e.Backend.ArchiveTarget(ctx, m)
			if err == nil {
				err = e.advance(ctx, m, "rollback_archived", digest)
			}
		case "rollback_archived":
			if err = e.Backend.RestoreSource(ctx, m); err == nil {
				err = e.advance(ctx, m, "rollback_restored", "")
			}
		case "rollback_restored":
			if err = e.Backend.PrepareRollback(ctx, m); err == nil {
				err = e.Backend.PauseTarget(ctx, m)
			}
			if err == nil {
				err = e.Store.CommitRuntimeRollback(ctx, id)
			}
			if err == nil && e.AfterPhase != nil {
				err = e.AfterPhase("rolled_back")
			}
		case "rolled_back":
			return nil
		default:
			return fmt.Errorf("rollback cannot resume from phase %s", m.Phase)
		}
		if err != nil {
			return fmt.Errorf("rollback phase %s: %w", m.Phase, err)
		}
	}
}
