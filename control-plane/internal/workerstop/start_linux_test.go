package workerstop

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startFixture(t *testing.T) (*stopFixture, StopMarker, *sql.DB) {
	f := fixture(t)
	freshStartupStorage(t, &f.held.config)
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

// Shared with test_boot_transition.py: journal interruption recovery must match
// Go's struct hash, not the sorted JSON map bytes written by the original stop.
func TestStartupMarkerHashMatchesBootTransitionContract(t *testing.T) {
	m := StopMarker{Version: 1, Phase: "preparing", ReceiptSHA256: strings.Repeat("1", 64), InventorySHA256: strings.Repeat("2", 64),
		Bindings: []store.WorkerStopBinding{{SandboxID: "stable", AppID: "app", RuntimeID: "provider", TemplateID: "reviewed", Domain: "private.invalid", ConfigSHA256: strings.Repeat("e", 64), OwnerSHA256: strings.Repeat("f", 64), ConfigRevision: 8}},
		QEMUPID:  10, QEMUStartTime: "123", WorkerBootID: "11111111-1111-1111-1111-111111111111", WorkerMachineID: strings.Repeat("a", 32), DataUUID: "44444444-4444-4444-4444-444444444444"}
	if hash(m) != "0d97a7af2fbb666c75ba16dda56d4c38865ae28975320d30579587cab3e68fd1" {
		t.Fatal("native startup marker hash contract changed")
	}
}

func TestExternalCleanRequiresPinnedVerifierAndActualReceiptResult(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native root artifact ownership")
	}
	dir := t.TempDir()
	verifier := filepath.Join(dir, "external.py")
	code := []byte("# reviewed external evidence verifier fixture\n")
	if e := os.WriteFile(verifier, code, 0700); e != nil {
		t.Fatal(e)
	}
	p := Proof{QEMUPID: 43, QEMUStartTime: "101", WorkerBootID: "old-boot"}
	receipt := CleanReceipt{Version: 1, State: "externally-stopped-clean", Proof: p, External: &ExternalCleanEvidence{Version: 1, Method: externalCleanMethod, OuterBootID: "11111111-1111-1111-1111-111111111111", SupervisorPID: 42, SupervisorStartTime: "100", QEMUPID: 43, QEMUStartTime: "101", WorkerBootID: "old-boot", EvidenceDirectory: dir, EvidenceSHA256: strings.Repeat("a", 64)}}
	raw, e := json.Marshal(receipt)
	if e != nil {
		t.Fatal(e)
	}
	sc := StartConfig{Version: 1, CleanReceipt: filepath.Join(dir, "receipt.json"), ExternalVerifierSHA256: fmt.Sprintf("%x", sha256.Sum256(code))}
	if e = os.WriteFile(sc.CleanReceipt, raw, 0600); e != nil {
		t.Fatal(e)
	}
	answer := map[string]any{"version": 1, "verified": true, "receipt_sha256": fmt.Sprintf("%x", sha256.Sum256(raw)), "method": externalCleanMethod, "qemu_pid": 43, "qemu_start_time": "101", "worker_boot_id": "old-boot"}
	calls := 0
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		calls++
		if len(args) != 4 || args[0] != "/usr/bin/python3" || args[1] != verifier || args[2] != "--verify" || args[3] != sc.CleanReceipt {
			t.Fatal("unreviewed command", args)
		}
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > 15*time.Second {
			t.Fatal("missing bounded deadline")
		}
		return json.Marshal(answer)
	}
	if e = verifyExternalCleanWith(context.Background(), sc, receipt, verifier, run); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("verifier not run")
	}
	for _, field := range []string{"verified", "receipt_sha256", "method", "qemu_pid", "qemu_start_time", "worker_boot_id"} {
		t.Run(field, func(t *testing.T) {
			old := answer[field]
			answer[field] = "wrong"
			defer func() { answer[field] = old }()
			if verifyExternalCleanWith(context.Background(), sc, receipt, verifier, run) == nil {
				t.Fatal("accepted mismatched verifier result")
			}
		})
	}
	for _, kind := range []string{"missing-pin", "changed-code", "changed-receipt", "schema-only", "other-path"} {
		t.Run(kind, func(t *testing.T) {
			c, r := sc, receipt
			before := calls
			switch kind {
			case "missing-pin":
				c.ExternalVerifierSHA256 = ""
			case "changed-code":
				c.ExternalVerifierSHA256 = strings.Repeat("b", 64)
			case "changed-receipt":
				r.GeneratedAt++
			case "schema-only":
				r.External = nil
			case "other-path":
				c.CleanReceipt = filepath.Join(dir, "other.json")
			}
			if verifyExternalCleanWith(context.Background(), c, r, verifier, run) == nil {
				t.Fatal("accepted invalid external proof")
			}
			if calls != before {
				t.Fatal("executed verifier before validating pinned inputs")
			}
		})
	}
	if e = verifyExternalCleanWith(context.Background(), sc, receipt, verifier, func(context.Context, ...string) ([]byte, error) { return nil, errors.New("fixture failure") }); e == nil {
		t.Fatal("verifier failure accepted")
	}
}
