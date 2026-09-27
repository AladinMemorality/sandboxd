package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// These methods are operator/backend only. The caller authenticates the app owner
// and uploads/verifies immutable objects before publishing their DB pointer.
type ProjectRevision struct {
	ID           string `json:"id"`
	AppID        string `json:"app_id"`
	SourceSHA256 string `json:"source_sha256"`
	ManifestJSON string `json:"manifest_json"`
	ObjectKey    string `json:"object_key"`
	SizeBytes    int64  `json:"size_bytes"`
}
type ProjectWorker struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	Draining     bool   `json:"draining"`
	Priority     int    `json:"priority"`
	CPUMillis    int    `json:"cpu_millis"`
	MemoryMB     int    `json:"memory_mb"`
	MaxActive    int    `json:"max_active"`
	MaxStarting  int    `json:"max_starting"`
	MinDiskMB    int    `json:"min_disk_mb"`
	RuntimeImage string `json:"runtime_image"`
}
type ProjectWorkerObservation struct {
	WorkerID          string
	BootID            string
	At                time.Time
	DiskAvailableMB   int
	MemoryAvailableMB int
	Healthy           bool
}
type ProjectDeployment struct {
	ID, AppID, RevisionID, WorkerID, WorkerBootID, RuntimeID, State string
	Generation                                                      int64
	CPUMillis, MemoryMB, DiskMB                                     int
}

type ProjectDeploymentInfo struct {
	HasSourceRevision    bool   `json:"has_source_revision"`
	DeploymentRevisionID string `json:"deployment_revision_id,omitempty"`
	DeploymentGeneration int64  `json:"deployment_generation,omitempty"`
	RevisionID           string `json:"revision_id,omitempty"`
	Generation           int64  `json:"generation,omitempty"`
	SourceSHA256         string `json:"source_sha256,omitempty"`
	WorkerID             string `json:"worker_id,omitempty"`
	Phase                string `json:"phase,omitempty"`
}

// ProjectDeploymentInfo is owner-agnostic for internal use. HTTP callers must
// first scope the durable app to their authenticated tenant.
func (s *Store) ProjectDeploymentInfo(ctx context.Context, appID string) (ProjectDeploymentInfo, error) {
	var out ProjectDeploymentInfo
	err := s.db.QueryRowContext(ctx, `SELECT h.revision_id,h.generation,r.source_sha256,COALESCE(d.worker_id,''),COALESCE(d.state,''),COALESCE(d.revision_id,''),COALESCE(d.generation,0) FROM project_deployment_head h JOIN project_revision r ON r.id=h.revision_id AND r.app_id=h.app_id LEFT JOIN project_deployment d ON d.app_id=h.app_id AND d.state<>'released' WHERE h.app_id=?`, appID).Scan(&out.RevisionID, &out.Generation, &out.SourceSHA256, &out.WorkerID, &out.Phase, &out.DeploymentRevisionID, &out.DeploymentGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.HasSourceRevision = true
	return out, nil
}

var projectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var imageDigest = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[a-f0-9]{64}$`)
var ErrProjectCapacity = errors.New("no healthy project worker has capacity")

// PublishProjectRevision advances only the expected generation; a slow uploader
// cannot overwrite a more recent checkpoint. Same-request retries are idempotent.
func (s *Store) PublishProjectRevision(ctx context.Context, r ProjectRevision, expected int64) error {
	digest, e := hex.DecodeString(r.SourceSHA256)
	if e != nil || len(digest) != 32 || !projectID.MatchString(r.ID) || !projectID.MatchString(r.AppID) || expected < 0 || r.SizeBytes <= 0 || r.SizeBytes > 256<<20 || len(r.ManifestJSON) > 1<<20 || !json.Valid([]byte(r.ManifestJSON)) || r.ObjectKey != fmt.Sprintf("projects/%s/revisions/%s/source.zip.enc", r.AppID, r.ID) {
		return errors.New("invalid project revision")
	}
	return s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var revision string
		var generation int64
		err = tx.QueryRowContext(ctx, `SELECT revision_id,generation FROM project_deployment_head WHERE app_id=?`, r.AppID).Scan(&revision, &generation)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if revision == r.ID && generation == expected+1 {
			var hash, manifest, key string
			var size int64
			err = tx.QueryRowContext(ctx, `SELECT source_sha256,manifest_json,object_key,size_bytes FROM project_revision WHERE id=? AND app_id=?`, r.ID, r.AppID).Scan(&hash, &manifest, &key, &size)
			if err != nil {
				return err
			}
			if hash != r.SourceSHA256 || manifest != r.ManifestJSON || key != r.ObjectKey || size != r.SizeBytes {
				return ErrConflict
			}
			return nil
		}
		if generation != expected {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO project_revision(id,app_id,source_sha256,manifest_json,object_key,size_bytes) VALUES(?,?,?,?,?,?)`, r.ID, r.AppID, r.SourceSHA256, r.ManifestJSON, r.ObjectKey, r.SizeBytes)
		if err != nil {
			return err
		}
		if expected == 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO project_deployment_head(app_id,revision_id,generation) VALUES(?,?,1)`, r.AppID, r.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE project_deployment_head SET revision_id=?,generation=generation+1 WHERE app_id=? AND generation=?`, r.ID, r.AppID, expected)
		}
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

// ConfigureProjectWorker never derives a budget from host RAM or a guest report.
// Reducing a budget is allowed: existing charges stay, new placements stop.
func (s *Store) ConfigureProjectWorker(ctx context.Context, w ProjectWorker) error {
	if !projectID.MatchString(w.ID) || w.CPUMillis < 1 || w.MemoryMB < 1 || w.MaxActive < 1 || w.MaxStarting < 1 || w.MaxStarting > w.MaxActive || w.MinDiskMB < 0 || !imageDigest.MatchString(w.RuntimeImage) {
		return errors.New("invalid worker budget or unpinned runtime image")
	}
	return s.submit(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO project_worker(id,enabled,draining,priority,cpu_millis,memory_mb,max_active,max_starting,min_disk_mb,runtime_image) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,draining=excluded.draining,priority=excluded.priority,cpu_millis=excluded.cpu_millis,memory_mb=excluded.memory_mb,max_active=excluded.max_active,max_starting=excluded.max_starting,min_disk_mb=excluded.min_disk_mb,runtime_image=excluded.runtime_image,healthy=0,observed_at=0`, w.ID, w.Enabled, w.Draining, w.Priority, w.CPUMillis, w.MemoryMB, w.MaxActive, w.MaxStarting, w.MinDiskMB, w.RuntimeImage)
		return err
	})
}

// ObserveProjectWorker accepts trusted host-agent measurements, never guest input.
func (s *Store) ObserveProjectWorker(ctx context.Context, o ProjectWorkerObservation) error {
	if !projectID.MatchString(o.WorkerID) || !projectID.MatchString(o.BootID) || o.At.IsZero() || o.At.After(time.Now().Add(time.Second)) || o.DiskAvailableMB < 0 || o.MemoryAvailableMB < 0 {
		return errors.New("invalid worker observation")
	}
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `UPDATE project_worker SET observed_at=?,boot_id=?,disk_available_mb=?,memory_available_mb=?,healthy=? WHERE id=? AND observed_at<=?`, o.At.UnixMilli(), o.BootID, o.DiskAvailableMB, o.MemoryAvailableMB, o.Healthy, o.WorkerID, o.At.UnixMilli())
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

// ReserveProjectDeployment is a durable charge BEFORE any provider call. This
// table is for explicitly enrolled projects, not a substitute for existing Cube
// admission. The provider adapter must enforce and verify the selected worker.
func (s *Store) ReserveProjectDeployment(ctx context.Context, d ProjectDeployment, runtimeImage string) (ProjectDeployment, error) {
	if !projectID.MatchString(d.ID) || !projectID.MatchString(d.AppID) || !projectID.MatchString(d.RevisionID) || d.Generation < 1 || d.CPUMillis < 1 || d.MemoryMB < 1 || d.DiskMB < 1 || !imageDigest.MatchString(runtimeImage) {
		return d, errors.New("invalid deployment reservation")
	}
	var out ProjectDeployment
	err := s.submit(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var revision string
		var generation int64
		if err = tx.QueryRowContext(ctx, `SELECT revision_id,generation FROM project_deployment_head WHERE app_id=?`, d.AppID).Scan(&revision, &generation); err != nil {
			return err
		}
		if revision != d.RevisionID || generation != d.Generation {
			return ErrConflict
		}
		var existing int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM project_deployment WHERE app_id=? AND state<>'released'`, d.AppID).Scan(&existing); err != nil {
			return err
		}
		if existing != 0 {
			return ErrConflict
		}
		now := time.Now().UnixMilli()
		// Reserve full configured guest resources; live host headroom is an additional
		// check. Pending boots are deducted from live headroom until they are observed.
		err = tx.QueryRowContext(ctx, `SELECT w.id,w.boot_id FROM project_worker w WHERE w.enabled=1 AND w.draining=0 AND w.healthy=1 AND w.boot_id<>'' AND w.runtime_image=? AND w.observed_at BETWEEN ? AND ?
  AND (SELECT count(*) FROM project_deployment d WHERE d.worker_id=w.id AND d.state<>'released')<w.max_active
  AND (SELECT count(*) FROM project_deployment d WHERE d.worker_id=w.id AND d.state IN ('reserved','preparing','uncertain'))<w.max_starting
  AND COALESCE((SELECT sum(cpu_millis) FROM project_deployment d WHERE d.worker_id=w.id AND d.state<>'released'),0)+?<=w.cpu_millis
  AND COALESCE((SELECT sum(memory_mb) FROM project_deployment d WHERE d.worker_id=w.id AND d.state<>'released'),0)+?<=w.memory_mb
  AND COALESCE((SELECT sum(memory_mb) FROM project_deployment d WHERE d.worker_id=w.id AND d.state IN ('reserved','preparing','uncertain')),0)+?<=w.memory_available_mb
  AND COALESCE((SELECT sum(disk_mb) FROM project_deployment d WHERE d.worker_id=w.id AND d.state<>'released'),0)+?+w.min_disk_mb<=w.disk_available_mb
  ORDER BY w.priority,w.id LIMIT 1`, runtimeImage, now-25000, now, d.CPUMillis, d.MemoryMB, d.MemoryMB, d.DiskMB).Scan(&d.WorkerID, &d.WorkerBootID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProjectCapacity
		}
		if err != nil {
			return err
		}
		d.State = "reserved"
		d.RuntimeID = ""
		_, err = tx.ExecContext(ctx, `INSERT INTO project_deployment(id,app_id,revision_id,generation,worker_id,worker_boot_id,cpu_millis,memory_mb,disk_mb,state) VALUES(?,?,?,?,?,?,?,?,?,?)`, d.ID, d.AppID, d.RevisionID, d.Generation, d.WorkerID, d.WorkerBootID, d.CPUMillis, d.MemoryMB, d.DiskMB, d.State)
		if err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		out = d
		return nil
	})
	return out, err
}

// TransitionProjectDeployment is called by the trusted provider adapter. A
// released transition asserts observed absence/stop, not a timeout. No automatic
// expiration exists. Activation is fenced by both project generation and boot.
func (s *Store) TransitionProjectDeployment(ctx context.Context, id, from, to, runtimeID, workerID, bootID string) error {
	allowed := map[string]map[string]bool{
		"reserved":  {"preparing": true, "uncertain": true, "released": true},
		"preparing": {"ready": true, "uncertain": true, "released": true},
		"ready":     {"active": true, "uncertain": true, "released": true},
		"active":    {"uncertain": true, "released": true},
		"uncertain": {"released": true},
	}
	if !allowed[from][to] || !projectID.MatchString(id) || !projectID.MatchString(workerID) || !projectID.MatchString(bootID) || (runtimeID != "" && !projectID.MatchString(runtimeID)) || ((to == "ready" || to == "active") && runtimeID == "") {
		return errors.New("invalid deployment transition")
	}
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `UPDATE project_deployment SET state=?,runtime_id=CASE WHEN runtime_id='' THEN ? ELSE runtime_id END,updated_at=unixepoch() WHERE id=? AND state=? AND worker_id=? AND worker_boot_id=? AND (runtime_id='' OR runtime_id=?)
  AND (? NOT IN ('ready','active') OR EXISTS(SELECT 1 FROM project_worker w WHERE w.id=project_deployment.worker_id AND w.boot_id=project_deployment.worker_boot_id AND w.healthy=1 AND w.observed_at BETWEEN ? AND ?))
  AND (?<>'active' OR EXISTS(SELECT 1 FROM project_deployment_head h WHERE h.app_id=project_deployment.app_id AND h.revision_id=project_deployment.revision_id AND h.generation=project_deployment.generation))`, to, runtimeID, id, from, workerID, bootID, runtimeID, to, time.Now().UnixMilli()-25000, time.Now().UnixMilli(), to)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
