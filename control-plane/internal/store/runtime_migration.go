package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RuntimeMigration retains the original Docker identity until an operator has
// verified the new provider. Both copies remain private to the same app owner.
// Credential ciphertext is deliberately absent from JSON/status output.
type RuntimeMigration struct {
	SandboxID             string
	Phase                 string
	Source                Sandbox
	SourcePreset          string
	ConfigFingerprint     string
	TargetPreset          string
	Binding               RuntimeBinding `json:"-"`
	ArchiveSHA256         string
	RollbackSHA256        string
	HistorySHA256         string
	RollbackHistorySHA256 string
}

func (s *Store) GetRuntimeMigration(ctx context.Context, id string) (*RuntimeMigration, error) {
	m := &RuntimeMigration{}
	var source string
	err := s.db.QueryRowContext(ctx, `SELECT sandbox_id,phase,source_json,source_preset,config_fingerprint,target_preset,runtime_id,template_id,domain,token_ciphertext,token_nonce,archive_sha256,rollback_sha256,history_sha256,rollback_history_sha256 FROM runtime_migration WHERE sandbox_id=?`, id).Scan(&m.SandboxID, &m.Phase, &source, &m.SourcePreset, &m.ConfigFingerprint, &m.TargetPreset, &m.Binding.RuntimeID, &m.Binding.TemplateID, &m.Binding.Domain, &m.Binding.TokenCiphertext, &m.Binding.TokenNonce, &m.ArchiveSHA256, &m.RollbackSHA256, &m.HistorySHA256, &m.RollbackHistorySHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(source), &m.Source); err != nil {
		return nil, err
	}
	m.Binding.SandboxID = id
	m.Binding.Provider = "cube"
	return m, nil
}

func (s *Store) HasIncompleteRuntimeMigrations(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_migration WHERE phase NOT IN ('complete','rolled_back','aborted')`).Scan(&n)
	return n > 0, err
}

// BeginRuntimeMigration must be called under the offline exclusive maintenance
// lock. Eligibility is rechecked transactionally rather than trusting inventory.
func (s *Store) BeginRuntimeMigration(ctx context.Context, id, preset, template, domain string) error {
	sb, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if sb.RuntimeProvider != "docker" || !sb.AppID.Valid {
		return ErrConflict
	}
	app, err := s.GetApp(ctx, sb.AppID.String)
	if err != nil {
		return err
	}
	current, err := s.CurrentSandboxForApp(ctx, app.ID)
	if err != nil {
		return err
	}
	if current.ID != id {
		return fmt.Errorf("only the current app sandbox can be migrated")
	}
	selected, err := s.AppUsesCube(ctx, app.ID)
	if err != nil {
		return err
	}
	if selected {
		return fmt.Errorf("app already selects Cube")
	}
	raw, err := json.Marshal(sb)
	if err != nil {
		return err
	}
	fingerprint, err := s.RuntimeConfigFingerprint(ctx, app.ID)
	if err != nil {
		return err
	}
	return s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task WHERE sandbox_id=? AND status='running'`, id).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("active tasks prevent migration")
		}
		var provider string
		if err = tx.QueryRowContext(ctx, `SELECT runtime_provider FROM sandbox WHERE id=?`, id).Scan(&provider); err != nil {
			return err
		}
		if provider != "docker" {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_migration(sandbox_id,phase,source_json,source_preset,config_fingerprint,target_preset,template_id,domain,updated_at) VALUES (?,'planned',?,?,?,?,?,?,?)`, id, string(raw), app.RuntimePreset.String, fingerprint, preset, template, domain, time.Now().Unix())
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_migration_task(sandbox_id,task_id) SELECT sandbox_id,task_id FROM task WHERE sandbox_id=?`, id); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (s *Store) IsMigratedDockerTask(ctx context.Context, id, taskID string) (bool, error) {
	var yes int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runtime_migration_task t JOIN runtime_migration m ON m.sandbox_id=t.sandbox_id WHERE t.sandbox_id=? AND t.task_id=? AND m.phase IN ('complete','rollback_started','rollback_archived','rollback_restored'))`, id, taskID).Scan(&yes)
	return yes == 1, err
}

func (s *Store) MigratedDockerTaskNeedsCompatibility(ctx context.Context, id, taskID string) (bool, error) {
	var yes int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runtime_migration_task t JOIN runtime_migration m ON m.sandbox_id=t.sandbox_id WHERE t.sandbox_id=? AND t.task_id=? AND m.history_sha256='')`, id, taskID).Scan(&yes)
	return yes == 1, err
}

// RollbackNeedsFreshAgentSession also protects retained runtimed binaries that
// predate imported-history markers. Submission/failed tasks cannot establish a
// usable new provider conversation; only a successful postrollback task can.
func (s *Store) RollbackNeedsFreshAgentSession(ctx context.Context, id, agent string) (bool, error) {
	var yes int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM runtime_migration m JOIN sandbox s ON s.id=m.sandbox_id
 WHERE m.sandbox_id=? AND m.phase='rolled_back' AND s.runtime_provider='docker'
 AND NOT EXISTS(SELECT 1 FROM task t WHERE t.sandbox_id=m.sandbox_id
 AND t.agent=? AND t.status='succeeded'
 AND NOT EXISTS(SELECT 1 FROM runtime_migration_task old WHERE old.sandbox_id=t.sandbox_id AND old.task_id=t.task_id)))`, id, agent).Scan(&yes)
	return yes == 1, err
}

func (s *Store) MigrationTaskIDs(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT task_id FROM task WHERE sandbox_id=? ORDER BY task_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var taskID string
		if err = rows.Scan(&taskID); err != nil {
			return nil, err
		}
		ids = append(ids, taskID)
	}
	return ids, rows.Err()
}

// RecordMigrationHistory binds a private history artifact to the exact phase
// before the workspace acknowledgement can advance or provider can switch.
func (s *Store) RecordMigrationHistory(ctx context.Context, id, phase, digest string, rollback bool) error {
	if digest == "" || (!rollback && phase != "quiesced") || (rollback && phase != "rollback_started") {
		return ErrConflict
	}
	column := "history_sha256"
	if rollback {
		column = "rollback_history_sha256"
	}
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `UPDATE runtime_migration SET `+column+`=?,updated_at=? WHERE sandbox_id=? AND phase=? AND (`+column+`='' OR `+column+`=?)`, digest, time.Now().Unix(), id, phase, digest)
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

func (s *Store) MigratedDockerTasks(ctx context.Context, id string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.task_id FROM runtime_migration_task t JOIN runtime_migration m ON m.sandbox_id=t.sandbox_id JOIN sandbox s ON s.id=t.sandbox_id WHERE t.sandbox_id=? AND s.runtime_provider='cube' AND m.phase='complete' AND m.history_sha256=''`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var task string
		if err = rows.Scan(&task); err != nil {
			return nil, err
		}
		out[task] = true
	}
	return out, rows.Err()
}

// RuntimeConfigFingerprint hashes encrypted rows, never plaintext secrets.
// Rollback to the retained Docker environment is eligible only while unchanged.
func (s *Store) RuntimeConfigFingerprint(ctx context.Context, appID string) (string, error) {
	return RuntimeConfigFingerprintDB(ctx, s.db, appID)
}

// RuntimeConfigFingerprintDB supports a mode=ro eligibility check while the
// daemon is serving requests. The offline mutator rechecks after taking its lock.
func RuntimeConfigFingerprintDB(ctx context.Context, db *sql.DB, appID string) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key,access_policy,value_ciphertext,value_nonce,value_plaintext,sensitive FROM app_config WHERE app_id=? AND access_policy IN ('runtime_access','both') ORDER BY key`, appID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var key, policy string
		var ciphertext, nonce []byte
		var plaintext sql.NullString
		var sensitive int
		if err = rows.Scan(&key, &policy, &ciphertext, &nonce, &plaintext, &sensitive); err != nil {
			return "", err
		}
		data, _ := json.Marshal([]any{key, policy, ciphertext, nonce, plaintext, sensitive})
		h.Write(data)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (s *Store) PrepareMigrationTargetCredential(ctx context.Context, id string, ciphertext, nonce []byte) error {
	return s.submit(ctx, func(db *sql.DB) error {
		r, err := db.ExecContext(ctx, `UPDATE runtime_migration SET phase='staging',token_ciphertext=?,token_nonce=?,updated_at=? WHERE sandbox_id=? AND phase='archived'`, ciphertext, nonce, time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

func (s *Store) AbortRuntimeMigration(ctx context.Context, id string) error {
	return s.submit(ctx, func(db *sql.DB) error {
		r, err := db.ExecContext(ctx, `UPDATE runtime_migration SET phase='aborted',updated_at=? WHERE sandbox_id=? AND phase IN ('planned','quiesced','archived','staged','imported','verified') AND EXISTS(SELECT 1 FROM sandbox WHERE id=? AND runtime_provider='docker')`, time.Now().Unix(), id, id)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

// AdvanceRuntimeMigration is a compare-and-swap. Side effects precede their
// acknowledgement and must be repeatable when the process dies between them.
func (s *Store) AdvanceRuntimeMigration(ctx context.Context, id, from, to, checksum string) error {
	allowed := map[string]string{"planned": "quiesced", "quiesced": "archived", "archived": "staging", "staged": "imported", "imported": "verified", "complete": "rollback_started", "rollback_started": "rollback_archived", "rollback_archived": "rollback_restored"}
	if allowed[from] != to {
		return ErrConflict
	}
	return s.submit(ctx, func(db *sql.DB) error {
		column := "archive_sha256"
		if from == "rollback_started" {
			column = "rollback_sha256"
		}
		query := `UPDATE runtime_migration SET phase=?,updated_at=? WHERE sandbox_id=? AND phase=?`
		args := []any{to, time.Now().Unix(), id, from}
		if checksum != "" {
			query = `UPDATE runtime_migration SET phase=?,updated_at=?,` + column + `=? WHERE sandbox_id=? AND phase=?`
			args = []any{to, time.Now().Unix(), checksum, id, from}
		}
		r, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err == nil && n != 1 {
			return ErrConflict
		}
		return err
	})
}

// SaveMigrationTarget stores the newly allocated remote identity before any
// import. An ambiguous create remains in staging and requires explicit adoption.
func (s *Store) SaveMigrationTarget(ctx context.Context, id string, b *RuntimeBinding) error {
	if b.RuntimeID == "" || len(b.TokenCiphertext) == 0 || len(b.TokenNonce) == 0 {
		return ErrConflict
	}
	return s.submit(ctx, func(db *sql.DB) error {
		r, err := db.ExecContext(ctx, `UPDATE runtime_migration SET phase='staged',runtime_id=?,token_ciphertext=?,token_nonce=?,updated_at=? WHERE sandbox_id=? AND phase='staging'`, b.RuntimeID, b.TokenCiphertext, b.TokenNonce, time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err == nil && n != 1 {
			return ErrConflict
		}
		return err
	})
}

func (s *Store) CommitRuntimeMigration(ctx context.Context, id string) error {
	m, err := s.GetRuntimeMigration(ctx, id)
	if err != nil {
		return err
	}
	if m.Phase != "verified" {
		return ErrConflict
	}
	return s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		r, err := tx.ExecContext(ctx, `UPDATE runtime_migration SET phase='complete',updated_at=? WHERE sandbox_id=? AND phase='verified'`, time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		r, err = tx.ExecContext(ctx, `UPDATE sandbox SET runtime_provider='cube',status='stopped',image=?,container_id=NULL,cgroup_path=NULL,container_ip=NULL,stopped_at=?,updated_at=? WHERE id=? AND runtime_provider='docker'`, "cube-template:"+m.Binding.TemplateID, time.Now().Unix(), time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, _ = r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		if err = insertRuntimeBinding(ctx, tx, id, &m.Binding); err != nil {
			return err
		}
		// Target config was verified under maintenance, so it exactly matches this
		// transaction's desired revision (zero or one for an initial binding).
		if _, err = tx.ExecContext(ctx, `UPDATE runtime_binding SET config_applied_revision=config_revision WHERE sandbox_id=?`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE app SET runtime_preset=? WHERE id=?`, m.TargetPreset, m.Source.AppID.String); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// CommitRuntimeRollback is impossible until the target's CURRENT workspace was
// copied back and verified. Restoring a stale provider pointer is not rollback.
func (s *Store) CommitRuntimeRollback(ctx context.Context, id string) error {
	m, err := s.GetRuntimeMigration(ctx, id)
	if err != nil {
		return err
	}
	if m.Phase != "rollback_restored" || m.RollbackSHA256 == "" {
		return ErrConflict
	}
	return s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		// Freeze every imported task ID, including tasks run on Cube, before
		// restoring the legacy runtime. None establishes a local CLI session.
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO runtime_migration_task(sandbox_id,task_id) SELECT sandbox_id,task_id FROM task WHERE sandbox_id=?`, id); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `UPDATE runtime_migration SET phase='rolled_back',updated_at=? WHERE sandbox_id=? AND phase='rollback_restored'`, time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		r, err = tx.ExecContext(ctx, `UPDATE sandbox SET runtime_provider='docker',status='stopped',image=?,workspace_img=?,workspace_mnt=?,container_id=?,cgroup_path=?,container_ip=NULL,stopped_at=?,updated_at=? WHERE id=? AND runtime_provider='cube'`, m.Source.Image, m.Source.WorkspaceImg, m.Source.WorkspaceMnt, m.Source.ContainerID, m.Source.CgroupPath, time.Now().Unix(), time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, _ = r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM runtime_binding WHERE sandbox_id=?`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM app_runtime WHERE app_id=?`, m.Source.AppID.String); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE app SET runtime_preset=? WHERE id=?`, m.SourcePreset, m.Source.AppID.String); err != nil {
			return err
		}
		return tx.Commit()
	})
}
