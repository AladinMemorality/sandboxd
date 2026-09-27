package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func (s *Store) admissionWorkerID() string {
	if s.admissionWorker == "" {
		return "vps"
	}
	return s.admissionWorker
}

// AdmissionPartition shares the database and writer with the root Store. The
// returned view owns neither: it cannot close them or bypass cross-worker app
// uniqueness. Configure each view once before exposing it to request handlers.
func (s *Store) AdmissionPartition(worker string) (cube.AdmissionStore, error) {
	if !projectID.MatchString(worker) {
		return nil, errors.New("invalid admission worker")
	}
	view := &Store{db: s.db, writes: s.writes, doneCh: s.doneCh, closeCh: s.closeCh,
		admissionWorker: worker, storageNow: s.storageNow, storageRead: s.storageRead}
	return &workerAdmission{view}, nil
}

type workerAdmission struct{ view *Store }

func (s *Store) ConfigureNodeIdentity(ctx context.Context, node string) error {
	return s.submit(ctx, func(db *sql.DB) error {
		if node != "" {
			if _, err := db.ExecContext(ctx, `INSERT INTO cube_worker_identity(worker_id,node_id) VALUES(?,?) ON CONFLICT(worker_id) DO NOTHING`, s.admissionWorkerID(), node); err != nil {
				return err
			}
		}
		var actual string
		err := db.QueryRowContext(ctx, `SELECT node_id FROM cube_worker_identity WHERE worker_id=?`, s.admissionWorkerID()).Scan(&actual)
		if errors.Is(err, sql.ErrNoRows) && node == "" {
			return nil
		}
		if err != nil {
			return err
		}
		if actual != node {
			return errors.New("Cube node differs from durable worker identity")
		}
		return nil
	})
}
func (w *workerAdmission) AdmissionLookup(ctx context.Context, id string) (cube.AdmissionRecord, error) {
	a, err := w.view.AdmissionLookup(ctx, id)
	if err == nil && a.WorkerID != w.view.admissionWorkerID() {
		return cube.AdmissionRecord{}, cube.ErrAdmissionUnknown
	}
	return a, err
}
func (w *workerAdmission) AdmissionLookupKey(ctx context.Context, key string) (cube.AdmissionRecord, error) {
	a, err := w.view.AdmissionLookupKey(ctx, key)
	if err == nil && a.WorkerID != w.view.admissionWorkerID() {
		return cube.AdmissionRecord{}, cube.ErrAdmissionPending
	}
	return a, err
}

func (w *workerAdmission) AdmissionPolicy(ctx context.Context, n int, p string) error {
	return w.view.AdmissionPolicy(ctx, n, p)
}
func (w *workerAdmission) ConfigureNodeIdentity(ctx context.Context, n string) error {
	return w.view.ConfigureNodeIdentity(ctx, n)
}
func (w *workerAdmission) ConfigureStorageGuard(ctx context.Context, c *cube.StorageGuardConfig) error {
	return w.view.ConfigureStorageGuard(ctx, c)
}
func (w *workerAdmission) AdmissionBegin(ctx context.Context, k, id, t, op, token string) (cube.AdmissionRecord, error) {
	return w.view.AdmissionBegin(ctx, k, id, t, op, token)
}
func (w *workerAdmission) AdmissionFinish(ctx context.Context, a cube.AdmissionRecord, id, state string) error {
	return w.view.AdmissionFinish(ctx, a, id, state)
}
func (w *workerAdmission) AdmissionObserveReleased(ctx context.Context, a cube.AdmissionRecord, deleted bool) error {
	return w.view.AdmissionObserveReleased(ctx, a, deleted)
}
