package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// BeginRuntimeRollback freezes the desired config before the target is stopped.
// All following phases recheck it. Credentials remain encrypted in app_config.
func (s *Store) BeginRuntimeRollback(ctx context.Context, id string) error {
	return s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var appID, original, phase string
		if err = tx.QueryRowContext(ctx, `SELECT s.app_id,m.config_fingerprint,m.phase FROM runtime_migration m JOIN sandbox s ON s.id=m.sandbox_id WHERE m.sandbox_id=?`, id).Scan(&appID, &original, &phase); err != nil {
			return err
		}
		if phase != "complete" {
			return ErrConflict
		}
		var active int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task WHERE sandbox_id=? AND status='running'`, id).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return errors.New("active task prevents rollback")
		}
		fingerprint, err := runtimeConfigFingerprint(ctx, tx, appID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE runtime_migration SET phase='rollback_started',rollback_config_fingerprint=?,rollback_recreate=?,updated_at=? WHERE sandbox_id=? AND phase='complete'`, fingerprint, fingerprint != original, time.Now().Unix(), id)
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

// RecordRetainedDocker acknowledges a verified rename, never the intent alone.
func (s *Store) RecordRetainedDocker(ctx context.Context, id, name string) error {
	if name == "" {
		return ErrConflict
	}
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `UPDATE runtime_migration SET retained_docker_name=?,updated_at=? WHERE sandbox_id=? AND phase='rollback_restored' AND rollback_recreate=1 AND (retained_docker_name='' OR retained_docker_name=?)`, name, time.Now().Unix(), id, name)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

func (s *Store) RetireMigrationDocker(ctx context.Context, id string) error {
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `UPDATE runtime_migration SET retained_docker_retired=1,updated_at=? WHERE sandbox_id=? AND phase='rolled_back' AND retained_docker_name<>''`, time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
