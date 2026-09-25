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
func (s *Store) WorkerStopInventory(ctx context.Context) (WorkerStopSnapshot, error) {
	return WorkerStopInventoryDB(ctx, s.db)
}

// WorkerStopInventoryDB also supports a strictly mode=ro operator inspection.
func WorkerStopInventoryDB(ctx context.Context, db *sql.DB) (WorkerStopSnapshot, error) {
	out := WorkerStopSnapshot{Bindings: []WorkerStopBinding{}}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var n int
	for _, query := range []string{`SELECT COUNT(*) FROM runtime_migration WHERE phase NOT IN ('complete','rolled_back','aborted')`, `SELECT COUNT(*) FROM task WHERE status='running'`, `SELECT COUNT(*) FROM cube_admission WHERE state='pending'`, `SELECT COUNT(*) FROM cube_recovery WHERE phase<>'complete'`, `SELECT COUNT(*) FROM cube_admission a WHERE a.state<>'deleted' AND NOT EXISTS(SELECT 1 FROM runtime_binding b WHERE b.provider='cube' AND b.runtime_id=a.runtime_id)`} {
		if e = tx.QueryRowContext(ctx, query).Scan(&n); e != nil {
			return out, e
		}
		if n != 0 {
			return out, errors.New("active task, pending recovery/allocation or unbound reservation prevents worker stop")
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT b.sandbox_id,s.app_id,b.runtime_id,b.template_id,b.domain,b.config_revision,a.owner_token FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id JOIN app a ON a.id=s.app_id WHERE b.provider='cube' AND s.runtime_provider='cube' ORDER BY b.sandbox_id`)
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

func (s *Store) WorkerStopAllReleased(ctx context.Context) error {
	var n int
	if e := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE charged<>0 OR state='pending'`).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return errors.New("charged or pending allocation remains after pause")
	}
	return nil
}
