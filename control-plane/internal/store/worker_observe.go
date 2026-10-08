package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// WorkerObservationDB performs only reads in a single transaction. The caller
// must open SQLite mode=ro/query_only; never use Store.Open for monitoring.
type WorkerObservation struct {
	Bindings          []WorkerStopBinding
	Admissions        []cube.AdmissionRecord
	MaxActive         int
	Profile           string
	ResourceBudget    *cube.ResourceBudget
	ResourceTemplates map[string]cube.AdmissionResources
	PendingRecovery   int
}

func WorkerObservationDB(ctx context.Context, db *sql.DB, worker ...string) (WorkerObservation, error) {
	out := WorkerObservation{Bindings: []WorkerStopBinding{}, Admissions: []cube.AdmissionRecord{}}
	scope, err := workerScope(worker)
	if err != nil {
		return out, err
	}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var other int
	if scope != "" {
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_binding b WHERE b.provider='cube' AND NOT EXISTS(SELECT 1 FROM cube_admission a WHERE a.runtime_id=b.runtime_id AND a.state<>'deleted')`).Scan(&other); e != nil {
			return out, e
		}
		if other != 0 {
			return out, errors.New("unaccounted binding prevents worker-scoped observation")
		}
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE worker_id<>'vps' AND state<>'deleted'`).Scan(&other); e != nil {
		return out, e
	}
	if scope == "" && other != 0 {
		return out, errors.New("multi-worker inventory requires worker-scoped lifecycle tooling")
	}
	if e = tx.QueryRowContext(ctx, `SELECT max_active,profile FROM cube_admission_policy WHERE singleton=1 AND worker_id=?`, func() string {
		if scope == "" {
			return "vps"
		}
		return scope
	}()).Scan(&out.MaxActive, &out.Profile); e != nil {
		return out, e
	}
	var contract string
	budgetWorker := scope
	if budgetWorker == "" {
		budgetWorker = "vps"
	}
	e = tx.QueryRowContext(ctx, `SELECT contract FROM cube_resource_budget WHERE worker_id=?`, budgetWorker).Scan(&contract)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil {
		var c resourceContract
		if e = json.Unmarshal([]byte(contract), &c); e != nil {
			return out, e
		}
		out.ResourceBudget = &c.Budget
		out.ResourceTemplates = c.Templates
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_recovery WHERE phase<>'complete'`).Scan(&out.PendingRecovery); e != nil {
		return out, e
	}
	if scope != "" {
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission a WHERE a.worker_id=? AND a.state<>'deleted' AND (a.charged<>0 OR a.state<>'released') AND NOT EXISTS(SELECT 1 FROM runtime_binding b WHERE b.runtime_id=a.runtime_id)`, scope).Scan(&other); e != nil {
			return out, e
		}
		if other != 0 {
			return out, errors.New("unbound reservation prevents scoped observation")
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT b.sandbox_id,s.app_id,b.runtime_id,b.template_id,b.domain,b.config_revision FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id WHERE b.provider='cube' AND s.runtime_provider='cube' AND (?='' OR b.runtime_id IN (SELECT runtime_id FROM cube_admission WHERE worker_id=?)) ORDER BY b.sandbox_id`, scope, scope)
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
	rows, e = tx.QueryContext(ctx, `SELECT a.admission_key,a.runtime_id,a.template_id,a.operation,a.token,a.state,a.charged FROM cube_admission a WHERE a.state<>'deleted' AND (?='' OR a.worker_id=?) AND (?='' OR EXISTS(SELECT 1 FROM runtime_binding b WHERE b.runtime_id=a.runtime_id)) ORDER BY a.admission_key`, scope, scope, scope)
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
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_binding b JOIN cube_runtime_quarantine q ON q.runtime_id=b.runtime_id WHERE b.provider='cube' AND (?='' OR b.runtime_id IN (SELECT runtime_id FROM cube_admission WHERE worker_id=?))`, scope, scope).Scan(&n); e != nil {
		return out, e
	}
	if n != 0 {
		return out, errors.New("bound provider is quarantined")
	}
	return out, nil
}
