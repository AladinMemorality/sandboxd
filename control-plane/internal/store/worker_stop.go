package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type WorkerStopBinding struct {
	SandboxID, AppID, RuntimeID, TemplateID, Domain, ConfigSHA256, OwnerSHA256 string
	ConfigRevision                                                             int64
}
type WorkerStopSnapshot struct {
	Bindings []WorkerStopBinding
	SHA256   string
}

// WorkerStopInventory is private operator metadata, never a tenant list route.
func (s *Store) WorkerStopInventory(ctx context.Context, worker ...string) (WorkerStopSnapshot, error) {
	return WorkerStopInventoryDB(ctx, s.db, worker...)
}

// WorkerStopInventoryDB also supports a strictly mode=ro operator inspection.
func WorkerStopInventoryDB(ctx context.Context, db *sql.DB, worker ...string) (WorkerStopSnapshot, error) {
	out := WorkerStopSnapshot{Bindings: []WorkerStopBinding{}}
	scope, err := workerScope(worker)
	if err != nil {
		return out, err
	}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var n int
	if scope == "" {
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE worker_id<>'vps' AND state<>'deleted'`).Scan(&n); e != nil {
			return out, e
		}
		if n != 0 {
			return out, errors.New("multi-worker inventory requires worker-scoped lifecycle tooling")
		}
	}
	if scope != "" {
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_binding b WHERE b.provider='cube' AND NOT EXISTS(SELECT 1 FROM cube_admission a WHERE a.runtime_id=b.runtime_id AND a.state<>'deleted')`).Scan(&n); e != nil {
			return out, e
		}
		if n != 0 {
			return out, errors.New("unaccounted binding prevents worker-scoped stop")
		}
	}
	for _, query := range []string{`SELECT COUNT(*) FROM runtime_migration WHERE phase NOT IN ('complete','rolled_back','aborted')`, `SELECT COUNT(*) FROM task WHERE status IN ('running','queued')`, `SELECT COUNT(*) FROM cube_admission WHERE state='pending'`, `SELECT COUNT(*) FROM cube_recovery WHERE phase<>'complete'`} {
		if e = tx.QueryRowContext(ctx, query).Scan(&n); e != nil {
			return out, e
		}
		if n != 0 {
			return out, errors.New("active task, pending recovery/allocation or unbound reservation prevents worker stop")
		}
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission a WHERE a.state<>'deleted' AND (?='' OR a.worker_id=?) AND NOT EXISTS(SELECT 1 FROM runtime_binding b WHERE b.provider='cube' AND b.runtime_id=a.runtime_id) AND (?='' OR a.charged<>0 OR a.state<>'released')`, scope, scope, scope).Scan(&n); e != nil {
		return out, e
	}
	if n != 0 {
		return out, errors.New("unbound reservation prevents worker stop")
	}
	rows, e := tx.QueryContext(ctx, `SELECT b.sandbox_id,s.app_id,b.runtime_id,b.template_id,b.domain,b.config_revision,a.owner_token FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id JOIN app a ON a.id=s.app_id WHERE b.provider='cube' AND s.runtime_provider='cube' AND (?='' OR b.runtime_id IN (SELECT runtime_id FROM cube_admission WHERE worker_id=?)) ORDER BY b.sandbox_id`, scope, scope)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v WorkerStopBinding
		var owner string
		if e = rows.Scan(&v.SandboxID, &v.AppID, &v.RuntimeID, &v.TemplateID, &v.Domain, &v.ConfigRevision, &owner); e != nil {
			rows.Close()
			return out, e
		}
		v.OwnerSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(owner)))
		out.Bindings = append(out.Bindings, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for i := range out.Bindings {
		out.Bindings[i].ConfigSHA256, e = recoveryConfigFingerprint(ctx, tx, out.Bindings[i].AppID)
		if e != nil {
			return out, e
		}
	}
	raw, _ := json.Marshal(out.Bindings)
	out.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}

func (s *Store) WorkerStopAllReleased(ctx context.Context, worker ...string) error {
	scope, err := workerScope(worker)
	if err != nil {
		return err
	}
	var n int
	if e := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE (charged<>0 OR state='pending') AND (?='' OR worker_id=?)`, scope, scope).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return errors.New("charged or pending allocation remains after pause")
	}
	return nil
}

// Omitted scope preserves the original single-worker contract.
func workerScope(worker []string) (string, error) {
	if len(worker) > 1 {
		return "", errors.New("one worker scope required")
	}
	if len(worker) == 0 || worker[0] == "" {
		return "", nil
	}
	if !projectID.MatchString(worker[0]) {
		return "", errors.New("invalid worker scope")
	}
	return worker[0], nil
}
