package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func (s *Store) storageTime() (cube.StorageClock, error) {
	if s.storageNow != nil {
		return s.storageNow()
	}
	return cube.ReadStorageClock()
}
func (s *Store) ConfigureStorageGuard(ctx context.Context, cfg *cube.StorageGuardConfig) error {
	if cfg != nil {
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	return s.submit(ctx, func(db *sql.DB) error {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, `UPDATE cube_admission_policy SET max_active=max_active WHERE singleton=1`); e != nil {
			return e
		}
		var contract string
		e = tx.QueryRowContext(ctx, `SELECT contract FROM cube_storage_policy WHERE singleton=1`).Scan(&contract)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if cfg == nil {
			if e == nil {
				return cube.ErrStorageUnavailable
			}
			s.storageGuard = nil
			return tx.Commit()
		}
		var max int
		if e2 := tx.QueryRowContext(ctx, `SELECT max_active FROM cube_admission_policy WHERE singleton=1`).Scan(&max); e2 != nil {
			return e2
		}
		if max > 4 {
			return cube.ErrStorageUnavailable
		}
		if errors.Is(e, sql.ErrNoRows) {
			var charged int
			if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(charged),0) FROM cube_admission`).Scan(&charged); e != nil {
				return e
			}
			if charged != 0 {
				return cube.ErrStorageUnavailable
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO cube_storage_policy(singleton,contract) VALUES(1,?)`, cfg.Contract()); e != nil {
				return e
			}
		} else if contract != cfg.Contract() {
			return cube.ErrStorageUnavailable
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		copy := *cfg
		s.storageGuard = &copy
		return nil
	})
}

// The epoch resets using releases AFTER observation START, including operations
// completed between measuring free space and reading the observation file.
func (s *Store) storageAdmit(ctx context.Context, tx *sql.Tx, key, token string, allocate bool) error {
	var contract, priorJSON, priorBoot string
	var generation, started, spent, lastClock int64
	e := tx.QueryRowContext(ctx, `SELECT contract,generation,observation_json,started_ns,spent_bytes,last_clock_ns,clock_boot_id FROM cube_storage_policy WHERE singleton=1`).Scan(&contract, &generation, &priorJSON, &started, &spent, &lastClock, &priorBoot)
	if errors.Is(e, sql.ErrNoRows) {
		if s.storageGuard != nil {
			return cube.ErrStorageUnavailable
		}
		return nil
	}
	if e != nil {
		return e
	}
	if s.storageGuard == nil || contract != s.storageGuard.Contract() {
		return cube.ErrStorageUnavailable
	}
	// A positively observed running runtime retains its existing disk reservation.
	// Refresh and running-delete must remain possible with an offline observer.
	// Get releases paused reservations first, so paused-delete/resume allocate.
	if !allocate {
		var grants int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_storage_grant WHERE admission_key=? AND released_ns IS NULL`, key).Scan(&grants); e != nil {
			return e
		}
		if grants == 0 {
			return cube.ErrStorageUnavailable
		}
		return nil
	}
	now, e := s.storageTime()
	if e != nil || now.BootID != s.storageGuard.OuterBootID {
		return cube.ErrStorageUnavailable
	}
	if now.BootID == priorBoot && now.NS < lastClock {
		return cube.ErrStorageUnavailable
	}
	read := s.storageRead
	if read == nil {
		read = cube.ReadStorageObservation
	}
	o, e := read(*s.storageGuard, now)
	if e != nil || o.Validate(*s.storageGuard, now) != nil {
		return cube.ErrStorageUnavailable
	}
	raw, _ := json.Marshal(o)
	if o.Generation < generation || (now.BootID == priorBoot && o.StartedNS < started) {
		return cube.ErrStorageUnavailable
	}
	if o.Generation == generation {
		if string(raw) != priorJSON {
			return cube.ErrStorageUnavailable
		}
	} else {
		if now.BootID == priorBoot && o.StartedNS <= started {
			return cube.ErrStorageUnavailable
		}
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes),0) FROM cube_storage_grant WHERE released_ns IS NULL OR (released_boot_id=? AND released_ns>=?)`, now.BootID, o.StartedNS).Scan(&spent); e != nil {
			return e
		}
	}
	free := o.InnerFreeBytes
	if o.OuterFreeBytes < free {
		free = o.OuterFreeBytes
	}
	debit := int64(0)
	if allocate {
		debit = cube.StorageGrant
	}
	if free < cube.StorageBaseline || spent > free-cube.StorageReserve || debit > free-cube.StorageReserve-spent {
		return cube.ErrStorageUnavailable
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO cube_storage_grant(token,admission_key,granted_ns,grant_boot_id,bytes) VALUES(?,?,?,?,?)`, token, key, now.NS, now.BootID, cube.StorageGrant); e != nil {
		return e
	}

	_, e = tx.ExecContext(ctx, `UPDATE cube_storage_policy SET generation=?,observation_json=?,started_ns=?,spent_bytes=?,last_clock_ns=?,clock_boot_id=? WHERE singleton=1`, o.Generation, string(raw), o.StartedNS, spent+debit, now.NS, now.BootID)
	return e
}

// Only an authoritative CAS closes grants. Backwards clock leaves grants open.
func (s *Store) storageRelease(ctx context.Context, tx *sql.Tx, key string) error {
	now, e := s.storageTime()
	if e != nil {
		return nil
	} // retain grant conservatively
	_, e = tx.ExecContext(ctx, `UPDATE cube_storage_grant SET released_ns=?,released_boot_id=? WHERE admission_key=? AND released_ns IS NULL AND (grant_boot_id<>? OR granted_ns<=?) AND ((SELECT clock_boot_id FROM cube_storage_policy WHERE singleton=1)<>? OR ?>=(SELECT last_clock_ns FROM cube_storage_policy WHERE singleton=1))`, now.NS, now.BootID, key, now.BootID, now.NS, now.BootID, now.NS)
	return e
}
