package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// ReplanAbortedRuntimeMigrationWithHome requires the same exclusive offline
// fence as Begin. The caller must also confirm the former remote target is gone.
// The old journal/task list stay durable; new archives use a fresh directory.
func (s *Store) ReplanAbortedRuntimeMigrationWithHome(ctx context.Context, id, preset, template, domain, home string) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	return s.beginRuntimeMigration(ctx, id, preset, template, domain, home, hex.EncodeToString(token[:]))
}

func retainAbortedAttempt(ctx context.Context, tx *sql.Tx, source *Sandbox) error {
	rows, err := tx.QueryContext(ctx, `SELECT * FROM runtime_migration WHERE sandbox_id=?`, source.ID)
	if err != nil {
		return err
	}
	columns, err := rows.Columns()
	if err != nil {
		rows.Close()
		return err
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if !rows.Next() {
		rows.Close()
		return ErrNotFound
	}
	if err = rows.Scan(pointers...); err != nil {
		rows.Close()
		return err
	}
	snapshot := map[string]any{}
	for i, name := range columns {
		snapshot[name] = values[i]
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if snapshot["phase"] != "aborted" {
		return errors.New("only an aborted migration can be replanned")
	}
	var original Sandbox
	raw, ok := snapshot["source_json"].(string)
	if !ok || json.Unmarshal([]byte(raw), &original) != nil || original.AppID != source.AppID || original.ContainerID != source.ContainerID {
		return errors.New("retained source identity changed")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM runtime_binding WHERE sandbox_id=?`, source.ID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrConflict
	}
	runtimeID, ok := snapshot["runtime_id"].(string)
	if !ok {
		return ErrConflict
	}
	if runtimeID != "" {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cube_admission WHERE runtime_id=? AND state='deleted' AND charged=0`, runtimeID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("former target deletion is not acknowledged")
		}
	}
	tasks, err := tx.QueryContext(ctx, `SELECT task_id FROM runtime_migration_task WHERE sandbox_id=? ORDER BY task_id`, source.ID)
	if err != nil {
		return err
	}
	ids := []string{}
	for tasks.Next() {
		var id string
		if err = tasks.Scan(&id); err != nil {
			tasks.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = tasks.Err(); err != nil {
		tasks.Close()
		return err
	}
	tasks.Close()
	journal, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	taskJSON, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_migration_attempt(sandbox_id,archive_generation,journal_json,task_ids_json,retained_at) VALUES(?,?,?,?,?)`, source.ID, snapshot["archive_generation"], string(journal), string(taskJSON), time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM runtime_migration_task WHERE sandbox_id=?`, source.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM runtime_migration WHERE sandbox_id=? AND phase='aborted'`, source.ID)
	return err
}
