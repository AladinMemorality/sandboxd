package store

import (
	"context"
	"database/sql"
	"time"
)

// RecordMigrationHome binds a validated private artifact to the current phase.
// A retry cannot silently replace the original recovery snapshot.
func (s *Store) RecordMigrationHome(ctx context.Context, id, phase, digest string, rollback bool) error {
	if len(digest) != 64 || (!rollback && phase != "quiesced") || (rollback && phase != "rollback_started") {
		return ErrConflict
	}
	column := "home_sha256"
	if rollback {
		column = "rollback_home_sha256"
	}
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `UPDATE runtime_migration SET `+column+`=?,updated_at=? WHERE sandbox_id=? AND phase=? AND home_manifest_json<>'' AND (`+column+`='' OR `+column+`=?)`, digest, time.Now().Unix(), id, phase, digest)
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
