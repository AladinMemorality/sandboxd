package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func admissionConfig(max int) cube.AdmissionConfig {
	return cube.AdmissionConfig{MaxActive: max, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}}
}
func admissionInput(id string) cube.CreateRequest {
	return cube.CreateRequest{TemplateID: "tpl-reviewed", Metadata: map[string]string{"sandboxd_id": id, "sandboxd_app_id": "app-" + id}, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}}
}
func newAdmissionClient(t *testing.T, s *Store, url string, max int) *cube.Client {
	t.Helper()
	c, err := cube.New(cube.Config{APIURL: url, APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ConfigureAdmission(context.Background(), s, admissionConfig(max)); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestCubeAdmissionBurstReusesAndReleasesOnlyVerifiedSlots(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	var mu sync.Mutex
	vms := map[string]*cube.Sandbox{}
	var posts atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/sandboxes" {
			var in cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&in)
			id := "vm-" + in.Metadata["sandboxd_id"]
			vms[id] = &cube.Sandbox{SandboxID: id, TemplateID: in.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: in.Metadata}
			posts.Add(1)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(vms[id])
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 {
			w.WriteHeader(400)
			return
		}
		vm := vms[parts[1]]
		if vm == nil {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(vm)
			return
		}
		if r.Method == "DELETE" {
			delete(vms, parts[1])
			w.WriteHeader(204)
			return
		}
		switch parts[2] {
		case "connect":
			vm.State = "running"
			json.NewEncoder(w).Encode(vm)
		case "pause":
			vm.State = "paused"
			w.WriteHeader(204)
		default:
			w.WriteHeader(400)
		}
	}))
	defer provider.Close()
	c := newAdmissionClient(t, s, provider.URL, 3)
	if _, err := c.CreateSnapshot(ctx, "vm-unreserved", cube.SnapshotRequest{}); err == nil {
		t.Fatal("raw memory snapshot bypassed admission")
	}
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.Create(ctx, admissionInput(fmt.Sprint(i)))
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, cube.ErrCapacityUnavailable) {
				t.Errorf("unexpected burst error %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 3 || posts.Load() != 3 {
		t.Fatalf("oversubscription successes=%d posts=%d", successes.Load(), posts.Load())
	}
	mu.Lock()
	var id string
	for key := range vms {
		id = key
		break
	}
	mu.Unlock()
	if _, err := c.Connect(ctx, id, cube.ConnectRequest{}); err != nil {
		t.Fatalf("running Connect consumed another slot: %v", err)
	}
	if err := c.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	pausedRetry := admissionInput("new-paused-id")
	mu.Lock()
	pausedRetry.Metadata["sandboxd_app_id"] = vms[id].Metadata["sandboxd_app_id"]
	mu.Unlock()
	if _, err := c.Create(ctx, pausedRetry); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("paused same-app allowed duplicate creation: %v", err)
	}
	if _, err := c.Create(ctx, admissionInput("replacement")); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, id); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("paused delete may wake an unreserved VM: %v", err)
	}
	if _, err := c.Connect(ctx, id, cube.ConnectRequest{}); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("paused resume bypassed capacity: %v", err)
	}
	// Provider auto-pause is released by ordinary authoritative maintenance GET.
	mu.Lock()
	vms["vm-replacement"].State = "paused"
	mu.Unlock()
	if _, err := c.Get(ctx, "vm-replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(ctx, id, cube.ConnectRequest{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	originalApp := vms[id].Metadata["sandboxd_app_id"]
	mu.Unlock()
	if err := c.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, id); err != nil {
		t.Fatalf("confirmed deleted runtime not idempotent: %v", err)
	}
	recreated := admissionInput("new-generation")
	recreated.Metadata["sandboxd_app_id"] = originalApp
	if _, err := c.Create(ctx, recreated); err != nil {
		t.Fatalf("confirmed delete blocks same app recreation: %v", err)
	}
	if err := c.Delete(ctx, "vm-new-generation"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateFromSnapshot(ctx, "tpl-reviewed", admissionInput("snapshot")); err != nil {
		t.Fatal(err)
	}
}
func TestCubeAdmissionPendingSurvivesRestartAndCancellation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "admission.db")
	dsn := "file:" + dbPath + "?_journal=WAL&_busy_timeout=5000&_fk=1"
	s, err := Open(ctx, dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		close(entered)
		<-release
		w.WriteHeader(500)
	}))
	defer provider.Close()
	c := newAdmissionClient(t, s, provider.URL, 3)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = c.Create(canceled, admissionInput("already-canceled")); err == nil {
		t.Fatal("accepted canceled operation")
	}
	callCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := c.Create(callCtx, admissionInput("uncertain")); done <- err }()
	<-entered
	stop()
	close(release)
	err = <-done
	if !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("uncertain create not explicit: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c = newAdmissionClient(t, s, provider.URL, 3)
	if _, err = c.Create(ctx, admissionInput("uncertain")); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("repeated uncertain POST allowed: %v", err)
	}
	if _, err = c.Create(ctx, admissionInput("another")); !errors.Is(err, cube.ErrCreationBusy) {
		t.Fatalf("restart lost pending global create fence: %v", err)
	}
	retry := admissionInput("new-ulid")
	retry.Metadata["sandboxd_app_id"] = "app-uncertain"
	if _, err = c.Create(ctx, retry); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("new ULID bypassed same-app pending reservation: %v", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("retried POST %d times", posts.Load())
	}
}
func TestCubeAdmissionStalePausedObservationCannotReleaseNewGeneration(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.AdmissionPolicy(ctx, 1, "test"); err != nil {
		t.Fatal(err)
	}
	created, err := s.AdmissionBegin(ctx, "sandbox:one", "", "tpl-reviewed", "create", "create-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionFinish(ctx, created, "vm-one", "active"); err != nil {
		t.Fatal(err)
	}
	before, err := s.AdmissionLookup(ctx, "vm-one")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.AdmissionBegin(ctx, before.Key, before.RuntimeID, before.TemplateID, "connect", "new-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionObserveReleased(ctx, before, false); err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionFinish(ctx, pending, "vm-one", "active"); err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionObserveReleased(ctx, before, false); err != nil {
		t.Fatal(err)
	}
	current, err := s.AdmissionLookup(ctx, "vm-one")
	if err != nil || current.Charged != 1 || current.State != "active" {
		t.Fatalf("stale GET freed allocation %+v %v", current, err)
	}
	if _, err = s.AdmissionBegin(ctx, "sandbox:two", "", "tpl-reviewed", "create", "other"); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("oversubscribed after stale observation: %v", err)
	}
}
func TestCubeAdmissionRejectsProfileChangesAndAmbiguousPause(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.AdmissionPolicy(ctx, 1, "cpu=2;memory_mb=2048"); err != nil {
		t.Fatal(err)
	}
	if err := s.AdmissionPolicy(ctx, 2, "cpu=2;memory_mb=2048"); err == nil {
		t.Fatal("silently raised persisted capacity")
	}
	a, err := s.AdmissionBegin(ctx, "sandbox:one", "", "tpl-reviewed", "create", "created")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionFinish(ctx, a, "vm-one", "active"); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"sandboxID":"vm-one","templateID":"tpl-reviewed","state":"running","cpuCount":2,"memoryMB":2048}`)
			return
		}
		w.WriteHeader(204)
	}))
	defer provider.Close()
	c := newAdmissionClient(t, s, provider.URL, 1)
	if err = c.Pause(ctx, "vm-one"); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("released unobserved pause %v", err)
	}
	if _, err = c.Connect(ctx, "vm-one", cube.ConnectRequest{}); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("overlapped ambiguous pause %v", err)
	}
	if _, err = c.Create(ctx, admissionInput("two")); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("pause freed capacity prematurely %v", err)
	}
}
func TestCubeAdmissionContextCanceledWaitingForWriter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.AdmissionPolicy(ctx, 1, "test"); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithTimeout(ctx, time.Nanosecond)
	defer cancel()
	<-canceled.Done()
	if _, err := s.AdmissionBegin(canceled, "sandbox:one", "", "tpl-reviewed", "create", "one"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestCubeAdmissionConcurrentIndependentAdaptersShareOneBudget(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "shared.db") + "?_journal=WAL&_busy_timeout=5000&_fk=1"
	first, err := Open(ctx, dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = first.AdmissionPolicy(ctx, 3, "test"); err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 == 0 {
				s = second
			}
			_, err := s.AdmissionBegin(ctx, fmt.Sprintf("app:%d", i), "", "tpl-reviewed", "create", fmt.Sprint(i))
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, cube.ErrCapacityUnavailable) {
				t.Errorf("unexpected reserve result %v", err)
			}
		}(i)
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("independent writers admitted simultaneous create workflows: %d", admitted.Load())
	}
	// Confirm the first provider allocation, then admit the other two slots.
	var firstKey string
	if err = first.DB().QueryRow(`SELECT admission_key FROM cube_admission`).Scan(&firstKey); err != nil {
		t.Fatal(err)
	}
	a, err := first.AdmissionLookupKey(ctx, firstKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.AdmissionFinish(ctx, a, "vm-initial", "active"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		a, err = second.AdmissionBegin(ctx, fmt.Sprintf("app:extra-%d", i), "", "tpl-reviewed", "create", fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		if err = second.AdmissionFinish(ctx, a, fmt.Sprintf("vm-extra-%d", i), "active"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = first.AdmissionBegin(ctx, "app:over-budget", "", "tpl-reviewed", "create", "over"); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatal("shared capacity bypassed", err)
	}
}
func TestCubeAdmissionUnreviewedOrOversizedTemplateCannotBecomeAccepted(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	var posts atomic.Int32
	var metadata map[string]string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var in cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&in)
			metadata = in.Metadata
			posts.Add(1)
			w.WriteHeader(201)
		}
		json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-big", TemplateID: "tpl-reviewed", State: "running", CPUCount: 2, MemoryMB: 4096, Metadata: metadata})
	}))
	defer provider.Close()
	c := newAdmissionClient(t, s, provider.URL, 1)
	in := admissionInput("unknown")
	in.TemplateID = "tpl-unreviewed"
	if _, err := c.Create(ctx, in); err == nil || posts.Load() != 0 {
		t.Fatal("unreviewed template allocated")
	}
	if _, err := c.Create(ctx, admissionInput("big")); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("oversized runtime accepted: %v", err)
	}
	if _, err := c.Create(ctx, admissionInput("next")); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatal("oversized uncertain allocation released")
	}
	bad := admissionConfig(1)
	bad.Templates["tpl-reviewed"] = cube.AdmissionResources{CPUCount: 2, MemoryMB: 4096}
	if err := c.ConfigureAdmission(ctx, s, bad); err == nil {
		t.Fatal("nonuniform profile accepted")
	}
}

func TestCubeAdmissionBrowserDisconnectFinishesReservedOperation(t *testing.T) {
	s := openTestStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var metadata map[string]string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var in cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&in)
			metadata = in.Metadata
			close(entered)
			<-release
			w.WriteHeader(201)
		}
		json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-disconnect", TemplateID: "tpl-reviewed", State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: metadata})
	}))
	defer provider.Close()
	c := newAdmissionClient(t, s, provider.URL, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Create(ctx, admissionInput("disconnect")); done <- err }()
	<-entered
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("browser disconnect stranded successful operation: %v", err)
	}
	row, err := s.AdmissionLookup(context.Background(), "vm-disconnect")
	if err != nil || row.State != "active" {
		t.Fatalf("successful outcome not durable %+v %v", row, err)
	}
}
func TestCubeAdmissionExplicitFencedRecoveryAndCreateAdoption(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	var remote cube.Sandbox
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("recovery retried mutation")
		}
		if remote.State == "deleted" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(remote)
	}))
	defer provider.Close()
	c := newAdmissionClient(t, s, provider.URL, 1)
	a, err := s.AdmissionBegin(ctx, "app:one", "", "tpl-reviewed", "create", "unique-create")
	if err != nil {
		t.Fatal(err)
	}
	remote = cube.Sandbox{SandboxID: "vm-one", TemplateID: "tpl-reviewed", State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_app_id": "one", "sandboxd_admission_operation": "wrong"}}
	if err = c.AdoptAdmission(ctx, "vm-one"); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatal("adopted different operation")
	}
	remote.Metadata["sandboxd_admission_operation"] = a.Token
	if err = c.AdoptAdmission(ctx, "vm-one"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.AdmissionBegin(ctx, a.Key, "vm-one", "tpl-reviewed", "connect", "uncertain-connect")
	if err != nil {
		t.Fatal(err)
	}
	remote.State = "paused"
	if _, err = c.Get(ctx, "vm-one"); err != nil {
		t.Fatal(err)
	}
	row, _ := s.AdmissionLookup(ctx, "vm-one")
	if row.State != "pending" || row.Charged != 1 {
		t.Fatal("GET released in-flight ambiguity")
	}
	if err = c.ReconcileAdmission(ctx, a.Key, false); err == nil {
		t.Fatal("recovery allowed without request drain")
	}
	if err = c.ReconcileAdmission(ctx, a.Key, true); err != nil {
		t.Fatal(err)
	}
	row, _ = s.AdmissionLookup(ctx, "vm-one")
	if row.State != "released" || row.Charged != 0 {
		t.Fatal("fenced paused state not reconciled")
	}
	if err = s.AdmissionFinish(ctx, pending, "vm-one", "active"); err == nil {
		t.Fatal("stale completion rewrote recovery")
	}
}

func TestCubeAdmissionRecoveryCannotIncreaseUnreservedChargeOverBudget(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.AdmissionPolicy(ctx, 1, "test"); err != nil {
		t.Fatal(err)
	}
	a, err := s.AdmissionBegin(ctx, "app:one", "", "tpl-reviewed", "create", "create")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionFinish(ctx, a, "vm-one", "released"); err != nil {
		t.Fatal(err)
	}
	deletion, err := s.AdmissionBegin(ctx, a.Key, "vm-one", "tpl-reviewed", "pause", "pause")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmissionBegin(ctx, "app:two", "", "tpl-reviewed", "create", "other"); err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionFinish(ctx, deletion, "vm-one", "active"); err == nil {
		t.Fatal("repair introduced an unreserved active allocation")
	}
	row, err := s.AdmissionLookup(ctx, "vm-one")
	if err != nil || row.State != "pending" || row.Charged != 0 {
		t.Fatalf("pending repair changed unexpectedly %+v %v", row, err)
	}
}

func TestCubeAdmissionCreateQueueCancellationAndCrossClientFence(t *testing.T) {
	s := openTestStore(t)
	verifying, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var posts atomic.Int32
	var mu sync.Mutex
	vms := map[string]cube.Sandbox{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var in cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&in)
			n := posts.Add(1)
			vm := cube.Sandbox{SandboxID: fmt.Sprintf("vm-%d", n), TemplateID: in.TemplateID, CPUCount: 2, MemoryMB: 2048, State: "running", Metadata: in.Metadata}
			mu.Lock()
			vms[vm.SandboxID] = vm
			mu.Unlock()
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(vm)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
		if id == "vm-1" {
			once.Do(func() { close(verifying) })
			<-release
		}
		mu.Lock()
		vm := vms[id]
		mu.Unlock()
		json.NewEncoder(w).Encode(vm)
	}))
	defer provider.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	c := newAdmissionClient(t, s, provider.URL, 3)
	other := newAdmissionClient(t, s, provider.URL, 3)
	finished := make(chan error, 1)
	go func() { _, err := c.Create(context.Background(), admissionInput("first")); finished <- err }()
	<-verifying
	queued, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, err := c.Create(queued, admissionInput("cancelled")); canceled <- err }()
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatal("queued cancellation ignored", err)
	}
	if _, err := other.Create(context.Background(), admissionInput("other")); !errors.Is(err, cube.ErrCreationBusy) {
		t.Fatal("other adapter bypassed pending provider verification", err)
	}
	if posts.Load() != 1 {
		t.Fatal("overlapping provider creates", posts.Load())
	}
	var rows int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cube_admission`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("waiting request charged a slot", rows, err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := other.Create(context.Background(), admissionInput("other")); err != nil {
		t.Fatal("acknowledgment did not release create gate", err)
	}
	if posts.Load() != 2 {
		t.Fatal(posts.Load())
	}
}

func TestCubeAdmissionPendingCreateDoesNotBlockRunningConnect(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	var connects atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sandboxes/vm-running" && r.URL.Path != "/sandboxes/vm-running/connect" {
			t.Error("unexpected provider mutation")
			w.WriteHeader(500)
			return
		}
		if r.Method == "POST" {
			connects.Add(1)
		}
		json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-running", TemplateID: "tpl-reviewed", CPUCount: 2, MemoryMB: 2048, State: "running"})
	}))
	defer provider.Close()
	client := newAdmissionClient(t, s, provider.URL, 3)
	active, err := s.AdmissionBegin(ctx, "app:running", "", "tpl-reviewed", "create", "original")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdmissionFinish(ctx, active, "vm-running", "active"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.AdmissionBegin(ctx, "app:pending", "", "tpl-reviewed", "create", "ambiguous")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Connect(ctx, "vm-running", cube.ConnectRequest{}); err != nil {
		t.Fatal("pending create blocked existing app", err)
	}
	row, err := s.AdmissionLookupKey(ctx, pending.Key)
	if err != nil || row.State != "pending" || row.Token != pending.Token || row.Charged != 1 {
		t.Fatal("connect changed pending create", row, err)
	}
	if connects.Load() != 1 {
		t.Fatal("running connect not forwarded")
	}
}

// A worker can lose its provider registration without deleting the underlying
// VM or disk. Missing registration must not free capacity or permit mutations.
func TestCubeAdmissionMissingKnownRuntimeRetainsReservation(t *testing.T) {
	for _, state := range []string{"active", "released", "pending"} {
		t.Run(state, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			var mutations atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations.Add(1)
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer provider.Close()
			c := newAdmissionClient(t, s, provider.URL, 1)
			a, err := s.AdmissionBegin(ctx, "app:one", "vm-one", "tpl-reviewed", "create", "create-one")
			if err != nil {
				t.Fatal(err)
			}
			if state != "pending" {
				if err = s.AdmissionFinish(ctx, a, "vm-one", state); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.AdmissionLookup(ctx, "vm-one")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Get(ctx, "vm-one"); !errors.Is(err, cube.ErrRuntimeUnavailable) {
				t.Fatalf("GET: %v", err)
			}
			if _, err = c.Connect(ctx, "vm-one", cube.ConnectRequest{}); !errors.Is(err, cube.ErrRuntimeUnavailable) {
				t.Fatalf("connect: %v", err)
			}
			if err = c.Pause(ctx, "vm-one"); !errors.Is(err, cube.ErrRuntimeUnavailable) {
				t.Fatalf("pause: %v", err)
			}
			if err = c.Delete(ctx, "vm-one"); !errors.Is(err, cube.ErrRuntimeUnavailable) {
				t.Fatalf("delete: %v", err)
			}
			after, err := s.AdmissionLookup(ctx, "vm-one")
			if err != nil || before != after {
				t.Fatalf("missing registration changed ledger: before=%+v after=%+v err=%v", before, after, err)
			}
			if before.Charged == 1 {
				if _, err = c.Create(ctx, admissionInput("two")); !errors.Is(err, cube.ErrCapacityUnavailable) {
					t.Fatalf("lost registration freed capacity: %v", err)
				}
			}
			if mutations.Load() != 0 {
				t.Fatalf("unexpected provider mutation count: %d", mutations.Load())
			}
			raw, err := cube.New(cube.Config{APIURL: provider.URL, APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = raw.Get(ctx, "vm-one")
			var upstream *cube.APIError
			if !errors.As(err, &upstream) || upstream.StatusCode != 404 {
				t.Fatalf("operator lost raw provider result: %v", err)
			}
		})
	}
}
