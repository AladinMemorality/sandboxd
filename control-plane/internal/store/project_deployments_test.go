package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const projectTestImage = "registry.example/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func projectFixture(t *testing.T, s *Store, id string) ProjectDeployment {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateApp(ctx, &App{ID: id, OwnerToken: "owner", Name: id}); err != nil {
		t.Fatal(err)
	}
	r := ProjectRevision{ID: "rev-" + id, AppID: id, SourceSHA256: strings.Repeat("a", 64), ManifestJSON: `{"version":1}`, ObjectKey: fmt.Sprintf("projects/%s/revisions/rev-%s/source.zip.enc", id, id), SizeBytes: 100}
	if err := s.PublishProjectRevision(ctx, r, 0); err != nil {
		t.Fatal(err)
	}
	return ProjectDeployment{ID: "deploy-" + id, AppID: id, RevisionID: r.ID, Generation: 1, CPUMillis: 2000, MemoryMB: 2048, DiskMB: 4096}
}
func projectWorkerFixture(t *testing.T, s *Store, id string, priority, max int) {
	t.Helper()
	ctx := context.Background()
	if err := s.ConfigureProjectWorker(ctx, ProjectWorker{ID: id, Enabled: true, Priority: priority, CPUMillis: 2000 * max, MemoryMB: 2048 * max, MaxActive: max, MaxStarting: max, MinDiskMB: 1024, RuntimeImage: projectTestImage}); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveProjectWorker(ctx, ProjectWorkerObservation{WorkerID: id, BootID: "boot-one", At: time.Now(), DiskAvailableMB: 100000, MemoryAvailableMB: 100000, Healthy: true}); err != nil {
		t.Fatal(err)
	}
}
func TestProjectFleetOverflowAndNoOversubscription(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	projectWorkerFixture(t, s, "vps", 1, 2)
	projectWorkerFixture(t, s, "b200", 2, 3)
	first := projectFixture(t, s, "first")
	got, err := s.ReserveProjectDeployment(ctx, first, projectTestImage)
	if err != nil || got.WorkerID != "vps" {
		t.Fatalf("first %+v %v", got, err)
	}
	var requests []ProjectDeployment
	for i := 0; i < 20; i++ {
		requests = append(requests, projectFixture(t, s, fmt.Sprintf("app-%d", i)))
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	for _, d := range requests {
		wg.Add(1)
		go func(d ProjectDeployment) {
			defer wg.Done()
			_, err := s.ReserveProjectDeployment(ctx, d, projectTestImage)
			if err == nil {
				count.Add(1)
			} else if !errors.Is(err, ErrProjectCapacity) {
				t.Errorf("reserve: %v", err)
			}
		}(d)
	}
	wg.Wait()
	if count.Load() != 4 {
		t.Fatalf("capacity count %d", count.Load())
	}
	if err = s.TransitionProjectDeployment(ctx, got.ID, "reserved", "uncertain", "", got.WorkerID, got.WorkerBootID); err != nil {
		t.Fatal(err)
	}
	d := projectFixture(t, s, "later")
	if _, err = s.ReserveProjectDeployment(ctx, d, projectTestImage); !errors.Is(err, ErrProjectCapacity) {
		t.Fatalf("uncertain charge disappeared: %v", err)
	}
	if err = s.TransitionProjectDeployment(ctx, got.ID, "uncertain", "released", "", got.WorkerID, got.WorkerBootID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveProjectDeployment(ctx, d, projectTestImage); err != nil {
		t.Fatal(err)
	}
}
func TestProjectRevisionFencesAndIdempotentRetry(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	d := projectFixture(t, s, "source")
	r := ProjectRevision{ID: "rev-source", AppID: d.AppID, SourceSHA256: strings.Repeat("a", 64), ManifestJSON: `{"version":1}`, ObjectKey: "projects/source/revisions/rev-source/source.zip.enc", SizeBytes: 100}
	if err := s.PublishProjectRevision(ctx, r, 0); err != nil {
		t.Fatal(err)
	}
	r.SizeBytes++
	if err := s.PublishProjectRevision(ctx, r, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	r.ID = "rev-new"
	r.ObjectKey = "projects/source/revisions/rev-new/source.zip.enc"
	if err := s.PublishProjectRevision(ctx, r, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation: %v", err)
	}
	projectWorkerFixture(t, s, "vps", 1, 2)
	got, err := s.ReserveProjectDeployment(ctx, d, projectTestImage)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishProjectRevision(ctx, r, 1); err != nil {
		t.Fatal(err)
	}
	info, err := s.ProjectDeploymentInfo(ctx, d.AppID)
	if err != nil || !info.HasSourceRevision || info.RevisionID != "rev-new" || info.DeploymentRevisionID != "rev-source" || info.Generation != 2 || info.DeploymentGeneration != 1 {
		t.Fatalf("conflated checkpoint and running deployment: %+v %v", info, err)
	}
	for _, transition := range [][2]string{{"reserved", "preparing"}, {"preparing", "ready"}} {
		if err = s.TransitionProjectDeployment(ctx, got.ID, transition[0], transition[1], "runtime-1", got.WorkerID, got.WorkerBootID); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.TransitionProjectDeployment(ctx, got.ID, "ready", "active", "runtime-1", got.WorkerID, got.WorkerBootID); !errors.Is(err, ErrConflict) {
		t.Fatalf("activated obsolete revision: %v", err)
	}
}
func TestProjectWorkerFreshnessBootAndDuplicateWriter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	d := projectFixture(t, s, "source")
	projectWorkerFixture(t, s, "vps", 1, 2)
	if _, err := s.DB().Exec(`UPDATE project_worker SET observed_at=observed_at-30000`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectDeployment(ctx, d, projectTestImage); !errors.Is(err, ErrProjectCapacity) {
		t.Fatalf("stale accepted: %v", err)
	}
	projectWorkerFixture(t, s, "vps", 1, 2)
	got, err := s.ReserveProjectDeployment(ctx, d, projectTestImage)
	if err != nil {
		t.Fatal(err)
	}
	d.ID = "duplicate"
	if _, err = s.ReserveProjectDeployment(ctx, d, projectTestImage); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate writer: %v", err)
	}
	if err = s.TransitionProjectDeployment(ctx, got.ID, "reserved", "preparing", "runtime-1", got.WorkerID, got.WorkerBootID); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveProjectWorker(ctx, ProjectWorkerObservation{WorkerID: "vps", BootID: "boot-two", At: time.Now(), DiskAvailableMB: 100000, MemoryAvailableMB: 100000, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.TransitionProjectDeployment(ctx, got.ID, "preparing", "ready", "runtime-1", got.WorkerID, got.WorkerBootID); !errors.Is(err, ErrConflict) {
		t.Fatalf("old boot became ready: %v", err)
	}
}

func TestProjectWorkerBudgetDrainingAndNoGPUProfile(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	d := projectFixture(t, s, "source")
	projectWorkerFixture(t, s, "vps", 1, 1)
	for _, change := range []string{`draining=1`, `enabled=0`, `disk_available_mb=4096`, `memory_available_mb=1024`, `cpu_millis=1000`, `runtime_image='unpinned'`} {
		t.Run(change, func(t *testing.T) {
			projectWorkerFixture(t, s, "vps", 1, 1)
			if _, err := s.DB().Exec(`UPDATE project_worker SET ` + change); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReserveProjectDeployment(ctx, d, projectTestImage); !errors.Is(err, ErrProjectCapacity) {
				t.Fatalf("unsafe placement: %v", err)
			}
		})
	}
}
