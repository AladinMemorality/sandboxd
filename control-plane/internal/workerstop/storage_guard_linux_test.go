package workerstop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
)

// Enroll the actual migrated store, create a real grant, then invalidate only
// the observer. The fake provider still exercises the concrete Cube HTTP client.
func guardedStopFixture(t *testing.T, missing bool) *stopFixture {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root-owned observation fixture")
	}
	f := fixture(t)
	ctx := context.Background()
	if e := f.client.Pause(ctx, "vm-0"); e != nil {
		t.Fatal(e)
	}
	clock, e := cube.ReadStorageClock()
	if e != nil {
		t.Fatal(e)
	}
	g := cube.StorageGuardConfig{ObservationPath: filepath.Join(t.TempDir(), "observation.json"), ObserverID: "11111111111111111111111111111111", WorkerMachineID: "22222222222222222222222222222222", ExpectedBootID: "33333333-3333-3333-3333-333333333333", OuterBootID: clock.BootID, InnerFSUUID: "44444444-4444-4444-4444-444444444444", OuterFSUUID: "55555555-5555-5555-5555-555555555555"}
	o := cube.StorageObservation{Version: 1, Generation: 1, ObserverID: g.ObserverID, WorkerMachineID: g.WorkerMachineID, WorkerBootID: g.ExpectedBootID, OuterBootID: g.OuterBootID, InnerFSUUID: g.InnerFSUUID, OuterFSUUID: g.OuterFSUUID, StartedNS: clock.NS, CompletedNS: clock.NS, InnerFreeBytes: cube.StorageBaseline, OuterFreeBytes: cube.StorageBaseline}
	write := func() {
		t.Helper()
		raw, _ := json.Marshal(o)
		if e := os.WriteFile(g.ObservationPath, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write()
	policy := f.held.config.Admission
	policy.StorageGuard = &g
	policy.WritableDiskMB = 10240
	if e = f.client.ConfigureAdmission(ctx, f.held.db, policy); e != nil {
		t.Fatal(e)
	}
	a, e := f.held.db.AdmissionBegin(ctx, "app:app-0", "vm-0", "tpl-reviewed", "connect", "guarded-running")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.held.db.AdmissionFinish(ctx, a, "vm-0", "active"); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	v := f.guests["vm-0"]
	v.State = "running"
	f.guests["vm-0"] = v
	f.mu.Unlock()
	f.held.config.Admission = policy
	if missing {
		if e = os.Remove(g.ObservationPath); e != nil {
			t.Fatal(e)
		}
	} else {
		if clock.NS <= int64(31*time.Second) {
			t.Skip("host just booted; stale-clock fixture requires31seconds uptime")
		}
		o.StartedNS = clock.NS - int64(31*time.Second)
		o.CompletedNS = o.StartedNS
		write()
	}
	// Restarted coordinator configuration does not require a fresh observation in
	// order to pause/reconcile; allocation will still refuse the stale/missing file.
	if e = f.client.ConfigureAdmission(ctx, f.held.db, policy); e != nil {
		t.Fatal(e)
	}
	return f
}

func TestWorkerStopEnrolledStorageGuardAllowsPauseWithStaleOrMissingObserver(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "stale"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			f := guardedStopFixture(t, missing)
			ctx := context.Background()
			proof, e := f.held.prepare(ctx, f.client, f.controller, f.sync, f.databaseUsers)
			if e != nil || !proof.Verified || f.pauses != 2 {
				t.Fatalf("guard blocked safe pause: pauses=%d err=%v", f.pauses, e)
			}
			if e = f.held.db.WorkerStopAllReleased(ctx); e != nil {
				t.Fatal(e)
			}
			if _, e = f.client.Connect(ctx, "vm-0", cube.ConnectRequest{}); !errors.Is(e, cube.ErrStorageUnavailable) {
				t.Fatal("stale/missing observer allowed a new wake", e)
			}
		})
	}
}
func TestWorkerStartEnrolledStorageGuardReconcilesWithoutWakingOnStaleObserver(t *testing.T) {
	f := guardedStopFixture(t, false)
	ctx := context.Background()
	if _, e := f.held.prepare(ctx, f.client, f.controller, f.sync, f.databaseUsers); e != nil {
		t.Fatal(e)
	}
	snap, e := f.held.db.WorkerStopInventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	m := StopMarker{Version: 1, Phase: "preparing", Bindings: snap.Bindings, InventorySHA256: snap.SHA256}
	marker, _ := maintenance.WorkerStopMarker(f.held.config.Database)
	if e = os.Remove(marker); e != nil {
		t.Fatal(e)
	}
	if e = maintenance.WriteWorkerStop(f.held.config.Database, m); e != nil {
		t.Fatal(e)
	}
	db, e := readDB(f.held.config)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var migrations int
	if e = db.QueryRow(`SELECT COUNT(*) FROM migration WHERE id=34`).Scan(&migrations); e != nil || migrations != 1 {
		t.Fatalf("fixture not enrolled with schema34: %d %v", migrations, e)
	}
	proof, e := reconcileStart(ctx, f.held.config, m, db, f.client, func() error { return nil }, func() error { return nil }, f.databaseUsers)
	if e != nil || !proof.TenantReady || proof.GuestsWoken != 0 || f.pauses != 2 {
		t.Fatal("stale observer blocked read-only startup reconciliation or woke a guest", e)
	}
	if e = maintenance.CheckWorkerStop(f.held.config.Database); e != nil {
		t.Fatal(e)
	}
	if _, e = f.client.Connect(ctx, "vm-0", cube.ConnectRequest{}); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("startup released storage admission gate", e)
	}
}
