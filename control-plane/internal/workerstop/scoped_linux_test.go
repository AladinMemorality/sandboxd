//go:build linux

package workerstop

import (
	"context"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"testing"
)

func TestRetainedInactiveInventoryCannotHideRunningGuest(t *testing.T) {
	for _, state := range []string{"paused", "stopped", "running", "unknown"} {
		rows, e := filterRetained([]cube.Sandbox{{SandboxID: "owned", State: "running"}, {SandboxID: "old", State: state}}, map[string]bool{"old": true})
		if state == "paused" || state == "stopped" {
			if e != nil || len(rows) != 1 || rows[0].SandboxID != "owned" {
				t.Fatal(rows, e)
			}
		} else if e == nil {
			t.Fatal("active retained guest hidden")
		}
	}
}

func TestScopedInventoryRetainsOnlyReleasedUnboundReservations(t *testing.T) {
	f := fixture(t)
	db := f.held.db.DB()
	ctx := context.Background()
	if _, e := db.Exec(`DELETE FROM runtime_binding WHERE runtime_id='vm-1'; UPDATE cube_admission SET state='released',charged=0 WHERE runtime_id='vm-1'`); e != nil {
		t.Fatal(e)
	}
	if o, e := store.WorkerObservationDB(ctx, db, "vps"); e != nil || len(o.Admissions) != 1 {
		t.Fatal(o, e)
	}
	if _, e := store.WorkerStopInventoryDB(ctx, db, "vps"); e != nil {
		t.Fatal(e)
	}
	if _, e := store.WorkerStopInventoryDB(ctx, db); e == nil {
		t.Fatal("legacy policy changed")
	}
	if _, e := db.Exec(`UPDATE cube_admission SET charged=1 WHERE runtime_id='vm-1'`); e != nil {
		t.Fatal(e)
	}
	if _, e := store.WorkerObservationDB(ctx, db, "vps"); e == nil {
		t.Fatal("charged orphan hidden")
	}
	if _, e := store.WorkerStopInventoryDB(ctx, db, "vps"); e == nil {
		t.Fatal("charged orphan ignored by stop")
	}
}

func TestWorkerScopedInventoryPreservesOtherWorkerReservation(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()
	db := f.held.db.DB()
	if _, e := db.Exec(`UPDATE cube_admission SET worker_id='b200',state='active',charged=1 WHERE runtime_id='vm-1'`); e != nil {
		t.Fatal(e)
	}
	if _, e := store.WorkerObservationDB(ctx, db); e == nil {
		t.Fatal("legacy observer accepted mixed fleet")
	}
	if _, e := store.WorkerStopInventoryDB(ctx, db); e == nil {
		t.Fatal("legacy stop accepted mixed fleet")
	}
	snap, e := store.WorkerStopInventoryDB(ctx, db, "vps")
	if e != nil || len(snap.Bindings) != 1 || snap.Bindings[0].RuntimeID != "vm-0" {
		t.Fatal(snap, e)
	}
	observation, e := store.WorkerObservationDB(ctx, db, "vps")
	if e != nil || len(observation.Bindings) != 1 || len(observation.Admissions) != 1 || observation.Admissions[0].RuntimeID != "vm-0" {
		t.Fatal(observation, e)
	}
	if _, e = db.Exec(`UPDATE cube_admission SET state='released',charged=0 WHERE runtime_id='vm-0'`); e != nil {
		t.Fatal(e)
	}
	if e = f.held.db.WorkerStopAllReleased(ctx, "vps"); e != nil {
		t.Fatal(e)
	}
	if e = f.held.db.WorkerStopAllReleased(ctx); e == nil {
		t.Fatal("other worker reservation disappeared")
	}
	if _, e = db.Exec(`DELETE FROM cube_admission WHERE runtime_id='vm-1'`); e != nil {
		t.Fatal(e)
	}
	if _, e = store.WorkerStopInventoryDB(ctx, db, "vps"); e == nil {
		t.Fatal("unaccounted binding hidden by scope")
	}
	if _, e = store.WorkerObservationDB(ctx, db, "vps"); e == nil {
		t.Fatal("unaccounted binding hidden by observer")
	}
}
