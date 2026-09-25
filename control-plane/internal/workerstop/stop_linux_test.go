package workerstop

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type stopFixture struct {
	held              *Held
	client            *cube.Client
	mu                sync.Mutex
	guests            map[string]cube.Sandbox
	pauses            int
	failPause         bool
	detailMissing     bool
	beforePause       func()
	syncs             int
	databaseChecks    int
	databaseFailureAt int
}

func fixture(t *testing.T) *stopFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, e := store.Open(ctx, "file:"+path+"?_journal=WAL&_busy_timeout=5000&_fk=1", "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	f := &stopFixture{guests: map[string]cube.Sandbox{}}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == "GET" && r.URL.Path == "/sandboxes" {
			out := []cube.Sandbox{}
			for _, v := range f.guests {
				out = append(out, v)
			}
			json.NewEncoder(w).Encode(out)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
		pause := strings.HasSuffix(id, "/pause")
		id = strings.TrimSuffix(id, "/pause")
		v, ok := f.guests[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if f.detailMissing && id == "vm-0" {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(v)
			return
		}
		if r.Method == "POST" && pause {
			f.pauses++
			if maintenance.CheckWorkerStop(path) == nil {
				t.Error("Pause before durable startup fence")
			}
			if f.beforePause != nil {
				f.beforePause()
			}
			if f.failPause {
				w.WriteHeader(503)
				return
			}
			v.State = "paused"
			f.guests[id] = v
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(405)
	}))
	t.Cleanup(provider.Close)
	c, e := cube.New(cube.Config{APIURL: provider.URL, APIKey: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	policy := cube.AdmissionConfig{MaxActive: 4, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}}
	if e = c.ConfigureAdmission(ctx, db, policy); e != nil {
		t.Fatal(e)
	}
	f.client = c
	for i, state := range []string{"running", "paused"} {
		id := fmt.Sprintf("stable-%d", i)
		app := fmt.Sprintf("app-%d", i)
		runtime := fmt.Sprintf("vm-%d", i)
		if e = db.CreateApp(ctx, &store.App{ID: app, OwnerToken: "fixture-owner"}); e != nil {
			t.Fatal(e)
		}
		a, e := db.AdmissionBegin(ctx, "app:"+app, "", "tpl-reviewed", "create", "create-"+id)
		if e != nil {
			t.Fatal(e)
		}
		admissionState := "active"
		if state == "paused" {
			admissionState = "released"
		}
		if e = db.AdmissionFinish(ctx, a, runtime, admissionState); e != nil {
			t.Fatal(e)
		}
		if e = db.Create(ctx, &store.Sandbox{ID: id, AppID: sql.NullString{String: app, Valid: true}, Status: "running", RuntimeProvider: "cube", RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: runtime, TemplateID: "tpl-reviewed", Domain: "cube.test", TokenCiphertext: []byte("encrypted"), TokenNonce: []byte("nonce")}}); e != nil {
			t.Fatal(e)
		}
		f.guests[runtime] = cube.Sandbox{SandboxID: runtime, TemplateID: "tpl-reviewed", Domain: "cube.test", State: state, CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_id": id, "sandboxd_app_id": app}}
	}
	snap, e := db.WorkerStopInventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	daemon, e := maintenance.Acquire(path, false)
	if e != nil {
		t.Fatal(e)
	}
	daemon.Close()
	lock, e := maintenance.Acquire(path, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = maintenance.WriteWorkerStop(path, map[string]string{"phase": "preparing", "inventory_sha256": snap.SHA256}); e != nil {
		t.Fatal(e)
	}
	f.held = &Held{lock: lock, db: db, marker: true, config: Config{Database: path, EvidenceDirectory: dir, Admission: policy, QEMUPID: 99, QEMUStartTime: "42", WorkerBootID: "fixture-boot"}, receipt: DrainReceipt{InventorySHA256: snap.SHA256}}
	t.Cleanup(func() { db.Close(); lock.Close() })
	return f
}

// The state-machine fixtures own this database and deliberately do not inspect
// unrelated host processes. The native /proc scanner has its own privileged
// maintenance integration test; production entrypoints always supply it.
func (f *stopFixture) databaseUsers(database string) error {
	if database != f.held.config.Database {
		return errors.New("foreign fixture database")
	}
	if _, err := os.Stat(database); err != nil {
		return err
	}
	f.databaseChecks++
	if f.databaseChecks == f.databaseFailureAt {
		return fmt.Errorf("fixture database descriptor inspection: %w", os.ErrPermission)
	}
	return nil
}
func (f *stopFixture) controller(context.Context, Config) error { return nil }
func (f *stopFixture) sync(context.Context, Config) error       { f.syncs++; return nil }
func TestWorkerStopAdmittedPausePreservesAlreadyPausedGuest(t *testing.T) {
	f := fixture(t)
	proof, e := f.held.prepare(context.Background(), f.client, f.controller, f.sync, f.databaseUsers)
	if e != nil {
		t.Fatal(e)
	}
	if !proof.Verified || len(proof.GuestStates) != 2 || f.pauses != 1 || f.syncs != 1 || f.databaseChecks != 3 {
		t.Fatalf("unexpected pause result: pauses=%d syncs=%d", f.pauses, f.syncs)
	}
	if e = f.held.db.WorkerStopAllReleased(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = maintenance.Acquire(f.held.config.Database, false); e == nil {
		t.Fatal("controller entered prepared stop")
	}
	if e = maintenance.CheckWorkerStop(f.held.config.Database); e == nil {
		t.Fatal("successful pause cleared startup fence")
	}
	info, e := os.Stat(f.held.evidencePath)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private proof missing")
	}
}
func TestWorkerStopUnknownMissingAndStoppedInventoryNeverPaused(t *testing.T) {
	for _, kind := range []string{"unknown", "missing", "stopped", "wrong-metadata"} {
		t.Run(kind, func(t *testing.T) {
			f := fixture(t)
			f.mu.Lock()
			switch kind {
			case "unknown":
				f.guests["unowned"] = cube.Sandbox{SandboxID: "unowned", State: "running"}
			case "missing":
				delete(f.guests, "vm-0")
			case "stopped":
				v := f.guests["vm-0"]
				v.State = "stopped"
				f.guests["vm-0"] = v
			case "wrong-metadata":
				v := f.guests["vm-0"]
				v.Metadata["sandboxd_app_id"] = "other-owner"
				f.guests["vm-0"] = v
			}
			f.mu.Unlock()
			if _, e := f.held.prepare(context.Background(), f.client, f.controller, f.sync, f.databaseUsers); e == nil {
				t.Fatal("unsafe inventory accepted")
			}
			if f.pauses != 0 || f.syncs != 0 {
				t.Fatal("mutated provider on incomplete inventory")
			}
		})
	}
}
func TestWorkerStopFailureRetainsLockMarkerAndPendingAllocation(t *testing.T) {
	f := fixture(t)
	f.failPause = true
	if _, e := f.held.prepare(context.Background(), f.client, f.controller, f.sync, f.databaseUsers); !errors.Is(e, cube.ErrAdmissionPending) {
		t.Fatalf("want pending error, got %v", e)
	}
	a, e := f.held.db.AdmissionLookup(context.Background(), "vm-0")
	if e != nil || a.State != "pending" || a.Charged != 1 {
		t.Fatal("ambiguous pause forgotten")
	}
	if f.syncs != 0 {
		t.Fatal("sync/powerdown proceeded after ambiguous pause")
	}
	if _, e = maintenance.Acquire(f.held.config.Database, false); e == nil {
		t.Fatal("failure dropped exclusion")
	}
	if e = maintenance.CheckWorkerStop(f.held.config.Database); e == nil {
		t.Fatal("failure cleared durable blocker")
	}
}
func TestWorkerStopConfigAndTaskChangesRefuse(t *testing.T) {
	for _, kind := range []string{"task", "config", "restart"} {
		t.Run(kind, func(t *testing.T) {
			f := fixture(t)
			ctx := context.Background()
			controller := f.controller
			switch kind {
			case "task":
				if e := f.held.db.CreateTask(ctx, &store.Task{TaskID: "new-task", SandboxID: "stable-0", Agent: "fixture"}); e != nil {
					t.Fatal(e)
				}
			case "config":
				if e := f.held.db.CreateAppConfig(ctx, &store.AppConfig{ID: "new", AppID: "app-0", Key: "MODE", AccessPolicy: "control_plane_only", ValuePlaintext: sql.NullString{String: "changed", Valid: true}}); e != nil {
					t.Fatal(e)
				}
			case "restart":
				controller = func(context.Context, Config) error { return errors.New("controller restarted") }
			}
			if _, e := f.held.prepare(ctx, f.client, controller, f.sync, f.databaseUsers); e == nil {
				t.Fatal("changed control state accepted")
			}
			if f.pauses != 0 {
				t.Fatal("paused despite changed state")
			}
		})
	}
}
func TestWorkerStopSyncFailureNeverReturnsPowerdownProof(t *testing.T) {
	f := fixture(t)
	if p, e := f.held.prepare(context.Background(), f.client, f.controller, func(context.Context, Config) error { return errors.New("syncfs failed") }, f.databaseUsers); e == nil || p != nil {
		t.Fatal("sync failure accepted")
	}
	if f.held.proof != nil {
		t.Fatal("failed sync persisted successful proof")
	}
	if e := maintenance.CheckWorkerStop(f.held.config.Database); e == nil {
		t.Fatal("marker removed")
	}
}
func TestWorkerStopReceiptFreshnessIdentityAndExternalTrustAreExplicit(t *testing.T) {
	now := time.Now()
	c := Config{ControllerID: strings.Repeat("a", 64), WorkerBootID: strings.Repeat("b", 32), QEMUPID: 42, QEMUStartTime: "100"}
	good := DrainReceipt{Version: 1, GeneratedAt: now, ControllerID: c.ControllerID, WorkerBootID: c.WorkerBootID, QEMUPID: 42, QEMUStartTime: "100", InventorySHA256: strings.Repeat("c", 64), CaddyConfigurationSHA256: strings.Repeat("d", 64), EvidenceSHA256: strings.Repeat("e", 64), TrafficFenced: true, ExistingRequestsDrained: true, DirectWritersFenced: true, ProviderJobsDrained: true}
	if e := validateReceipt(c, good, now); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*DrainReceipt){func(r *DrainReceipt) { r.GeneratedAt = now.Add(-3 * time.Minute) }, func(r *DrainReceipt) { r.ExistingRequestsDrained = false }, func(r *DrainReceipt) { r.QEMUStartTime = "101" }, func(r *DrainReceipt) { r.WorkerBootID = "other" }} {
		r := good
		change(&r)
		if e := validateReceipt(c, r, now); e == nil {
			t.Fatal("invalid drain receipt accepted")
		}
	}
}
func TestQEMUProcessGenerationRejectsAbsentPID(t *testing.T) {
	if QEMUAlive(Config{QEMUPID: 2147483647, QEMUStartTime: "1"}) {
		t.Fatal("nonexistent process alive")
	}
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", os.Getpid()))
	if e != nil {
		t.Fatal(e)
	}
	end := strings.LastIndex(string(raw), ")")
	fields := strings.Fields(string(raw[end+1:]))
	if !QEMUAlive(Config{QEMUPID: os.Getpid(), QEMUStartTime: fields[19]}) {
		t.Fatal("current exact process not observed")
	}
	if QEMUAlive(Config{QEMUPID: os.Getpid(), QEMUStartTime: "0"}) {
		t.Fatal("reused PID accepted")
	}
}

func TestWorkerStopMissingDetailRetainsKnownAllocation(t *testing.T) {
	f := fixture(t)
	f.detailMissing = true
	p, e := f.held.prepare(context.Background(), f.client, f.controller, f.sync, f.databaseUsers)
	if !errors.Is(e, cube.ErrRuntimeUnavailable) || p != nil {
		t.Fatalf("missing known detail must require recovery: %v", e)
	}
	if f.pauses != 0 || f.syncs != 0 {
		t.Fatal("mutation after known GET404")
	}
	a, e := f.held.db.AdmissionLookup(context.Background(), "vm-0")
	if e != nil || a.State != "active" || a.Charged != 1 {
		t.Fatal("GET404 silently released known allocation")
	}
	if maintenance.CheckWorkerStop(f.held.config.Database) == nil {
		t.Fatal("startup fence disappeared")
	}
}

func TestWorkerStopDatabaseScanFailureRetainsFenceAndNeverProvesPowerdown(t *testing.T) {
	for _, failAt := range []int{1, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			f := fixture(t)
			f.databaseFailureAt = failAt
			p, e := f.held.prepare(context.Background(), f.client, f.controller, f.sync, f.databaseUsers)
			if !errors.Is(e, os.ErrPermission) || p != nil || f.held.proof != nil {
				t.Fatalf("scan refusal not propagated: %v", e)
			}
			if failAt == 1 && (f.pauses != 0 || f.syncs != 0) {
				t.Fatal("mutation before initial descriptor check")
			}
			if failAt == 3 && (f.pauses != 1 || f.syncs != 1) {
				t.Fatal("did not exercise final post-sync descriptor check")
			}
			if maintenance.CheckWorkerStop(f.held.config.Database) == nil {
				t.Fatal("descriptor refusal cleared fence")
			}
			if f.held.evidencePath != "" {
				t.Fatal("descriptor refusal published powerdown evidence")
			}
		})
	}
}
