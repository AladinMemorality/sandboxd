package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// WorkerObservationDB performs only reads in a single transaction. The caller
// must open SQLite mode=ro/query_only; never use Store.Open for monitoring.
type WorkerObservation struct {
	Bindings        []WorkerStopBinding
	Admissions      []cube.AdmissionRecord
	MaxActive       int
	Profile         string
	PendingRecovery int
}

func WorkerObservationDB(ctx context.Context, db *sql.DB) (WorkerObservation, error) {
	out := WorkerObservation{Bindings: []WorkerStopBinding{}, Admissions: []cube.AdmissionRecord{}}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = tx.QueryRowContext(ctx, `SELECT max_active,profile FROM cube_admission_policy WHERE singleton=1`).Scan(&out.MaxActive, &out.Profile); e != nil {
		return out, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_recovery WHERE phase<>'complete'`).Scan(&out.PendingRecovery); e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT b.sandbox_id,s.app_id,b.runtime_id,b.template_id,b.domain,b.config_revision FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id WHERE b.provider='cube' AND s.runtime_provider='cube' ORDER BY b.sandbox_id`)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var b WorkerStopBinding
		if e = rows.Scan(&b.SandboxID, &b.AppID, &b.RuntimeID, &b.TemplateID, &b.Domain, &b.ConfigRevision); e != nil {
			rows.Close()
			return out, e
		}
		out.Bindings = append(out.Bindings, b)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT a.admission_key,a.runtime_id,a.template_id,a.operation,a.token,a.state,a.charged FROM cube_admission a WHERE a.state<>'deleted' ORDER BY a.admission_key`)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var a cube.AdmissionRecord
		if e = rows.Scan(&a.Key, &a.RuntimeID, &a.TemplateID, &a.Operation, &a.Token, &a.State, &a.Charged); e != nil {
			rows.Close()
			return out, e
		}
		out.Admissions = append(out.Admissions, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	var n int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_binding b JOIN cube_runtime_quarantine q ON q.runtime_id=b.runtime_id WHERE b.provider='cube'`).Scan(&n); e != nil {
		return out, e
	}
	if n != 0 {
		return out, errors.New("bound provider is quarantined")
	}
	return out, nil
}
