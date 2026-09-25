package workerstop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startFixture(t *testing.T) (*stopFixture, StopMarker, *sql.DB) {
	f := fixture(t)
	ctx := context.Background()
	if _, e := f.held.prepare(ctx, f.client, f.controller, f.sync, f.databaseUsers); e != nil {
		t.Fatal(e)
	}
	snap, e := f.held.db.WorkerStopInventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	m := StopMarker{Version: 1, Phase: "preparing", Bindings: snap.Bindings, InventorySHA256: snap.SHA256}
	path, _ := maintenance.WorkerStopMarker(f.held.config.Database)
	os.Remove(path)
	if e = maintenance.WriteWorkerStop(f.held.config.Database, m); e != nil {
		t.Fatal(e)
	}
	db, e := readDB(f.held.config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return f, m, db
}
func TestStartupReconcileKeepsGuestsPausedAndRemovesExactMarker(t *testing.T) {
	f, m, db := startFixture(t)
	calls := 0
	proof, e := reconcileStart(context.Background(), f.held.config, m, db, f.client, func() error { calls++; return nil }, func() error { return nil }, f.databaseUsers)
	if e != nil {
		t.Fatal(e)
	}
	if !proof.TenantReady || proof.GuestsWoken != 0 || proof.RoutingChanged || calls != 2 || f.pauses != 1 || f.databaseChecks != 6 {
		t.Fatal("unexpected mutation or verification")
	}
	if e = maintenance.CheckWorkerStop(f.held.config.Database); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(f.held.config.EvidenceDirectory, "startup-current.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE cube_admission SET charged=1`); e == nil {
		t.Fatal("monitor connection allows writes")
	}
}
func TestStartupRefusalsRetainMarker(t *testing.T) {
	for _, kind := range []string{"missing-admission", "pending", "deleted", "quarantine", "running", "unknown", "config", "ready-failure", "marker-replaced"} {
		t.Run(kind, func(t *testing.T) {
			f, m, db := startFixture(t)
			raw, e := sql.Open("sqlite3", "file:"+f.held.config.Database)
			if e != nil {
				t.Fatal(e)
			}
			defer raw.Close()
			switch kind {
			case "missing-admission":
				_, e = raw.Exec(`DELETE FROM cube_admission WHERE runtime_id='vm-0'`)
			case "pending":
				_, e = raw.Exec(`UPDATE cube_admission SET state='pending',charged=1 WHERE runtime_id='vm-0'`)
			case "quarantine":
				_, e = raw.Exec(`INSERT INTO cube_runtime_quarantine(runtime_id,recovery_id,sandbox_id,created_at) VALUES('vm-0','fixture-recovery','stable-0',1)`)
			case "deleted":
				_, e = raw.Exec(`UPDATE cube_admission SET state='deleted' WHERE runtime_id='vm-0'`)
			case "running":
				v := f.guests["vm-0"]
				v.State = "running"
				f.guests["vm-0"] = v
			case "unknown":
				delete(f.guests, "vm-0")
			case "config":
				m.InventorySHA256 = "changed"
			case "marker-replaced":
				p, _ := maintenance.WorkerStopMarker(f.held.config.Database)
				os.Remove(p)
				e = maintenance.WriteWorkerStop(f.held.config.Database, map[string]string{"replacement": "different"})
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = reconcileStart(context.Background(), f.held.config, m, db, f.client, func() error {
				if kind == "ready-failure" {
					return context.DeadlineExceeded
				}
				return nil
			}, func() error { return nil }, f.databaseUsers)
			if e == nil {
				t.Fatal("unsafe start accepted")
			}
			if maintenance.CheckWorkerStop(f.held.config.Database) == nil {
				t.Fatal("startup marker cleared on refusal")
			}
		})
	}
}
func TestStartupCleanReceiptBindsOldGenerationAndNewWorkerBoot(t *testing.T) {
	now := time.Now()
	bindings := []store.WorkerStopBinding{}
	m := StopMarker{Version: 1, Phase: "preparing", Bindings: bindings, InventorySHA256: hash(bindings), ReceiptSHA256: hash("receipt"), WorkerMachineID: "machine", WorkerBootID: "old", DataUUID: "data", QEMUPID: 10, QEMUStartTime: "20"}
	p := Proof{Version: 1, Verified: true, GeneratedAt: now.Add(-time.Minute), WorkerMachineID: m.WorkerMachineID, DataUUID: m.DataUUID, WorkerBootID: m.WorkerBootID, QEMUPID: m.QEMUPID, QEMUStartTime: m.QEMUStartTime, InventorySHA256: m.InventorySHA256, ReceiptSHA256: m.ReceiptSHA256, GuestStates: map[string]string{}}
	r := CleanReceipt{Version: 1, State: "stopped-clean", GeneratedAt: float64(now.Add(-time.Second).Unix()), Proof: p}
	c := Config{WorkerMachineID: "machine", DataUUID: "data", WorkerBootID: "new", QEMUPID: 11, QEMUStartTime: "30"}
	if e := validateStartProof(c, m, p, r, now); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"same-boot", "data", "receipt", "partial", "future", "same-process"} {
		t.Run(kind, func(t *testing.T) {
			cc, rr, pp := c, r, p
			switch kind {
			case "same-boot":
				cc.WorkerBootID = "old"
			case "data":
				cc.DataUUID = "other"
			case "receipt":
				rr.Proof.ReceiptSHA256 = "wrong"
			case "partial":
				pp.Verified = false
			case "future":
				rr.GeneratedAt = float64(now.Add(time.Hour).Unix())
			case "same-process":
				cc.QEMUPID = m.QEMUPID
				cc.QEMUStartTime = m.QEMUStartTime
			}
			if validateStartProof(cc, m, pp, rr, now) == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}
func TestReadonlyObservationRejectsUnchargedRunningAndFifthSlot(t *testing.T) {
	f := fixture(t)
	db, e := readDB(f.held.config)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	o, e := store.WorkerObservationDB(context.Background(), db)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := f.client.Inventory(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidateObservation(o, actual, f.held.config.Admission, false); e != nil {
		t.Fatal(e)
	}
	o.Admissions[0].Charged = 0
	if ValidateObservation(o, actual, f.held.config.Admission, false) == nil {
		t.Fatal("running uncharged allocation accepted")
	}
	// Five independently charged correct guests cannot pass the tested four-slot contract.
	o.Bindings = nil
	o.Admissions = nil
	actual = nil
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("running-%d", i)
		app := fmt.Sprintf("app-%d", i)
		o.Bindings = append(o.Bindings, store.WorkerStopBinding{SandboxID: id, RuntimeID: id, AppID: app, TemplateID: "tpl-reviewed"})
		o.Admissions = append(o.Admissions, cube.AdmissionRecord{Key: "app:" + app, RuntimeID: id, TemplateID: "tpl-reviewed", State: "active", Token: "token", Charged: 1})
		actual = append(actual, cube.Sandbox{SandboxID: id, TemplateID: "tpl-reviewed", State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_id": id, "sandboxd_app_id": app}})
	}
	if ValidateObservation(o, actual, f.held.config.Admission, false) == nil {
		t.Fatal("fifth active allocation accepted")
	}
}

func TestStartupDatabaseScanFailureNeverClearsMarker(t *testing.T) {
	for _, failAt := range []int{1, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			f, m, db := startFixture(t)
			f.databaseChecks = 0
			f.databaseFailureAt = failAt
			readyCalls := 0
			proof, e := reconcileStart(context.Background(), f.held.config, m, db, f.client, func() error { readyCalls++; return nil }, func() error { return nil }, f.databaseUsers)
			if !errors.Is(e, os.ErrPermission) || proof != nil {
				t.Fatalf("scan refusal not propagated: %v", e)
			}
			if failAt == 1 && readyCalls != 0 {
				t.Fatal("readiness before initial descriptor check")
			}
			if failAt == 3 && readyCalls != 2 {
				t.Fatal("did not exercise final post-readiness descriptor check")
			}
			if maintenance.CheckWorkerStop(f.held.config.Database) == nil {
				t.Fatal("scan refusal cleared marker")
			}
			if _, e := os.Stat(filepath.Join(f.held.config.EvidenceDirectory, "startup-current.json")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("scan refusal published readiness")
			}
			if f.pauses != 1 {
				t.Fatal("startup mutated provider")
			}
		})
	}
}
