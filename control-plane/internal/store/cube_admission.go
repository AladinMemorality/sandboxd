package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// AdmissionPolicy is immutable once installed. Raising capacity requires a
// separate reviewed operator transition, never a silent process-local override.
func (s *Store) AdmissionPolicy(ctx context.Context, maximum int, profile string) error {
	return s.submit(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO cube_admission_policy(singleton,max_active,profile) VALUES(1,?,?) ON CONFLICT(singleton) DO NOTHING`, maximum, profile)
		if err != nil {
			return err
		}
		var n int
		var p string
		if err = db.QueryRowContext(ctx, `SELECT max_active,profile FROM cube_admission_policy WHERE singleton=1`).Scan(&n, &p); err != nil {
			return err
		}
		if n != maximum || p != profile {
			return errors.New("Cube admission policy differs from durable capacity contract")
		}
		var missing int
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_binding b WHERE b.provider='cube' AND NOT EXISTS(SELECT 1 FROM cube_admission a WHERE a.runtime_id=b.runtime_id)`).Scan(&missing); err != nil {
			return err
		}
		if missing != 0 {
			return errors.New("existing Cube bindings require explicit admission inventory before activation")
		}
		return nil
	})
}
func scanAdmission(row *sql.Row) (cube.AdmissionRecord, error) {
	var a cube.AdmissionRecord
	err := row.Scan(&a.Key, &a.RuntimeID, &a.TemplateID, &a.Operation, &a.Token, &a.State, &a.Charged)
	return a, err
}
func (s *Store) AdmissionLookup(ctx context.Context, runtimeID string) (cube.AdmissionRecord, error) {
	return scanAdmission(s.db.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged FROM cube_admission WHERE runtime_id=?`, runtimeID))
}

// AdmissionBegin serializes count/check/reserve in the same SQLite write
// transaction, including independently constructed adapters sharing the DB.
func (s *Store) AdmissionBegin(ctx context.Context, key, runtimeID, templateID, operation, token string) (cube.AdmissionRecord, error) {
	var out cube.AdmissionRecord
	err := s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		// Acquire the SQLite write lock before reading count or previous state.
		if _, err = tx.ExecContext(ctx, `UPDATE cube_admission_policy SET max_active=max_active WHERE singleton=1`); err != nil {
			return err
		}
		var max int
		if err = tx.QueryRowContext(ctx, `SELECT max_active FROM cube_admission_policy WHERE singleton=1`).Scan(&max); err != nil {
			return err
		}
		previous, err := scanAdmission(tx.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged FROM cube_admission WHERE admission_key=?`, key))
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		recreate := exists && operation == "create" && previous.State == "deleted"
		if exists && !recreate && (previous.State == "pending" || operation == "create" || previous.RuntimeID != runtimeID || previous.TemplateID != templateID) {
			return cube.ErrAdmissionPending
		}
		if !exists && operation != "create" {
			return cube.ErrAdmissionUnknown
		}
		charge := previous.Charged
		// Upstream destroy can resume a paused VM before deleting it.
		if operation == "create" || operation == "connect" || operation == "delete" {
			charge = 1
		}
		if charge > previous.Charged {
			var used int
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(charged),0) FROM cube_admission`).Scan(&used); err != nil {
				return err
			}
			if used >= max {
				return cube.ErrAdmissionCapacity
			}
		}
		if operation == "create" {
			var pending int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE operation='create' AND state='pending'`).Scan(&pending); err != nil {
				return err
			}
			if pending != 0 {
				return cube.ErrCreationBusy
			}
		}
		out = cube.AdmissionRecord{Key: key, RuntimeID: runtimeID, TemplateID: templateID, Operation: operation, Token: token, State: "pending", Charged: charge}
		if exists {
			_, err = tx.ExecContext(ctx, `UPDATE cube_admission SET runtime_id=?,template_id=?,operation=?,token=?,state='pending',charged=? WHERE admission_key=?`, runtimeID, templateID, operation, token, charge, key)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO cube_admission(admission_key,runtime_id,template_id,operation,token,state,charged) VALUES(?,?,?,?,?,'pending',?)`, key, runtimeID, templateID, operation, token, charge)
		}
		if err != nil {
			return err
		}
		return tx.Commit()
	})
	return out, err
}
func (s *Store) AdmissionFinish(ctx context.Context, a cube.AdmissionRecord, runtimeID, state string) error {
	return s.submit(ctx, func(db *sql.DB) error {
		charge := 1
		if state == "released" || state == "deleted" {
			charge = 0
		} else if state != "active" {
			return errors.New("invalid Cube admission completion")
		}
		result, err := db.ExecContext(ctx, `UPDATE cube_admission SET runtime_id=?,state=?,charged=? WHERE admission_key=? AND token=? AND state='pending' AND (charged>=? OR (SELECT COALESCE(SUM(charged),0) FROM cube_admission) < (SELECT max_active FROM cube_admission_policy WHERE singleton=1))`, runtimeID, state, charge, a.Key, a.Token, charge)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return cube.ErrAdmissionPending
		}
		return nil
	})
}

// Observation release is a CAS against the generation read BEFORE GET. It
// cannot release a new Connect or an ambiguous pending operation.
func (s *Store) AdmissionObserveReleased(ctx context.Context, a cube.AdmissionRecord, deleted bool) error {
	state := "released"
	if deleted {
		state = "deleted"
	}
	return s.submit(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `UPDATE cube_admission SET state=?,charged=0 WHERE admission_key=? AND token=? AND state IN ('active','released')`, state, a.Key, a.Token)
		return err
	})
}

func (s *Store) AdmissionLookupKey(ctx context.Context, key string) (cube.AdmissionRecord, error) {
	return scanAdmission(s.db.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged FROM cube_admission WHERE admission_key=?`, key))
}
