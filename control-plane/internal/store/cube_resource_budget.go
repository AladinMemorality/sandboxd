package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

type resourceContract struct {
	Budget    cube.ResourceBudget                `json:"budget"`
	Templates map[string]cube.AdmissionResources `json:"templates"`
}

// ConfigureResourceBudget pins the exact template contracts durably. Changes
// need an explicit operator transition, not a restart with a larger limit.
func (s *Store) ConfigureResourceBudget(ctx context.Context, cfg cube.AdmissionConfig) error {
	if cfg.ResourceBudget != nil {
		raw, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if _, err = cube.ParseAdmissionConfig(string(raw)); err != nil {
			return err
		}
	}
	return s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `UPDATE cube_admission_policy SET max_active=max_active WHERE worker_id=?`, s.admissionWorkerID()); err != nil {
			return err
		}
		var prior string
		err = tx.QueryRowContext(ctx, `SELECT contract FROM cube_resource_budget WHERE worker_id=?`, s.admissionWorkerID()).Scan(&prior)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if cfg.ResourceBudget == nil {
			if err == nil {
				return errors.New("cannot disable durable resource budget")
			}
			return tx.Commit()
		}
		c := resourceContract{Budget: *cfg.ResourceBudget, Templates: cfg.Templates}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		var profile string
		var slots int
		if err = tx.QueryRowContext(ctx, `SELECT profile,max_active FROM cube_admission_policy WHERE worker_id=?`, s.admissionWorkerID()).Scan(&profile, &slots); err != nil {
			return err
		}
		if profile != "resource-budget-v1" || slots != cfg.MaxActive {
			return errors.New("resource budget differs from durable admission policy")
		}
		if prior != "" && prior != string(raw) {
			return errors.New("resource budget differs from durable template contract")
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT template_id FROM cube_admission WHERE worker_id=? AND state<>'deleted'`, s.admissionWorkerID())
		if err != nil {
			return err
		}
		for rows.Next() {
			var template string
			if err = rows.Scan(&template); err != nil {
				rows.Close()
				return err
			}
			if _, ok := c.Templates[template]; !ok {
				rows.Close()
				return errors.New("existing runtime has no resource contract")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO cube_resource_budget(worker_id,contract) VALUES(?,?) ON CONFLICT(worker_id) DO NOTHING`, s.admissionWorkerID(), string(raw)); err != nil {
			return err
		}
		if err = s.resourceAdmit(ctx, tx, "", "", 0); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (w *workerAdmission) ConfigureResourceBudget(ctx context.Context, cfg cube.AdmissionConfig) error {
	return w.view.ConfigureResourceBudget(ctx, cfg)
}

func (s *Store) resourceContract(ctx context.Context, tx *sql.Tx) (*resourceContract, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT contract FROM cube_resource_budget WHERE worker_id=?`, s.admissionWorkerID()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		var profile string
		if err = tx.QueryRowContext(ctx, `SELECT profile FROM cube_admission_policy WHERE worker_id=?`, s.admissionWorkerID()).Scan(&profile); err != nil {
			return nil, err
		}
		if profile == "resource-budget-v1" {
			return nil, errors.New("durable resource budget missing")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c resourceContract
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// resourceAdmit runs under the same SQLite write transaction as lifecycle
// admission. Pending/ambiguous operations stay charged across client restarts.
// Replacing key's reservation is also used by fenced runtime recovery.
func (s *Store) resourceAdmit(ctx context.Context, tx *sql.Tx, key, template string, charge int) error {
	c, err := s.resourceContract(ctx, tx)
	if err != nil || c == nil {
		return err
	}
	var cpu, mem, runtimes, builds int
	add := func(id string, n int) error {
		r, ok := c.Templates[id]
		p, hasProfile := c.Budget.Profiles[id]
		if !ok || !hasProfile {
			return cube.ErrAdmissionUnknown
		}
		cpu += n * p.CPUMillis
		mem += n * (r.MemoryMB + cube.VMOverheadMB)
		if p.Kind == "build" {
			builds += n
		} else if p.Kind == "runtime" {
			runtimes += n
		} else {
			return cube.ErrAdmissionUnknown
		}
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT template_id,SUM(charged) FROM cube_admission WHERE worker_id=? AND charged>0 AND admission_key<>? GROUP BY template_id`, s.admissionWorkerID(), key)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var n int
		if err = rows.Scan(&id, &n); err != nil {
			rows.Close()
			return err
		}
		if err = add(id, n); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if charge > 0 {
		if err = add(template, charge); err != nil {
			return err
		}
	}
	if cpu > c.Budget.CPUMillis || mem > c.Budget.MemoryMB || runtimes > c.Budget.RuntimeSlots || builds > c.Budget.BuildSlots {
		return cube.ErrCapacityUnavailable
	}
	return nil
}

func (s *Store) storageGrantBytes(ctx context.Context, tx *sql.Tx, template string) (int64, error) {
	c, err := s.resourceContract(ctx, tx)
	if err != nil {
		return 0, err
	}
	if c == nil {
		return cube.StorageGrant, nil
	}
	r, ok := c.Templates[template]
	p, hasProfile := c.Budget.Profiles[template]
	if !ok || !hasProfile {
		return 0, cube.ErrAdmissionUnknown
	}
	return int64(r.MemoryMB+p.WritableDiskMB) * 1024 * 1024, nil
}
