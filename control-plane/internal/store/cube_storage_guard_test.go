package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func storageConfig() cube.StorageGuardConfig {
	return cube.StorageGuardConfig{OuterBootID: "66666666-6666-6666-6666-666666666666", ObservationPath: "/run/cube-storage/observation.json", ObserverID: "11111111111111111111111111111111", WorkerMachineID: "22222222222222222222222222222222", ExpectedBootID: "33333333-3333-3333-3333-333333333333", InnerFSUUID: "44444444-4444-4444-4444-444444444444", OuterFSUUID: "55555555-5555-5555-5555-555555555555"}
}
func observation(cfg cube.StorageGuardConfig, now time.Time) cube.StorageObservation {
	return cube.StorageObservation{OuterBootID: cfg.OuterBootID, Version: 1, ObserverID: cfg.ObserverID, Generation: 1, StartedNS: now.Add(-time.Second).UnixNano(), CompletedNS: now.UnixNano(), WorkerMachineID: cfg.WorkerMachineID, WorkerBootID: cfg.ExpectedBootID, InnerFSUUID: cfg.InnerFSUUID, OuterFSUUID: cfg.OuterFSUUID, InnerFreeBytes: cube.StorageBaseline, OuterFreeBytes: cube.StorageBaseline}
}
func storageSetup(t *testing.T, s *Store) (*cube.StorageObservation, *time.Time) {
	t.Helper()
	ctx := context.Background()
	cfg := storageConfig()
	now := time.Unix(1800000000, 0)
	o := observation(cfg, now)
	s.storageNow = func() (cube.StorageClock, error) {
		return cube.StorageClock{BootID: cfg.OuterBootID, NS: now.UnixNano()}, nil
	}
	s.storageRead = func(cube.StorageGuardConfig, cube.StorageClock) (cube.StorageObservation, error) { return o, nil }
	if e := s.AdmissionPolicy(ctx, 4, "cpu=2;memory_mb=2048"); e != nil {
		t.Fatal(e)
	}
	if e := s.ConfigureStorageGuard(ctx, &cfg); e != nil {
		t.Fatal(e)
	}
	return &o, &now
}
func storageCreate(t *testing.T, s *Store, id string) cube.AdmissionRecord {
	t.Helper()
	a, e := s.AdmissionBegin(context.Background(), id, "", "tpl", "create", "create-"+id)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AdmissionFinish(context.Background(), a, "vm-"+id, "active"); e != nil {
		t.Fatal(e)
	}
	a.RuntimeID = "vm-" + id
	a.State = "active"
	return a
}
func storagePause(t *testing.T, s *Store, a cube.AdmissionRecord) {
	t.Helper()
	a, e := s.AdmissionBegin(context.Background(), a.Key, a.RuntimeID, a.TemplateID, "pause", "pause-"+a.Key)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AdmissionFinish(context.Background(), a, a.RuntimeID, "released"); e != nil {
		t.Fatal(e)
	}
}
func storageSpent(t *testing.T, s *Store) int64 {
	t.Helper()
	var n int64
	if e := s.db.QueryRow(`SELECT spent_bytes FROM cube_storage_policy WHERE singleton=1`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestStorageEpochCannotRecyclePausedCapacity(t *testing.T) {
	s := openTestStore(t)
	o, now := storageSetup(t, s)
	for i := 0; i < 4; i++ {
		storagePause(t, s, storageCreate(t, s, fmt.Sprint(i)))
	}
	if _, e := s.AdmissionBegin(context.Background(), "fifth", "", "tpl", "create", "fifth"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatalf("CPU empty must not reset disk epoch: %v", e)
	}
	if storageSpent(t, s) != 4*cube.StorageGrant {
		t.Fatal("release refunded epoch")
	}
	// Observe started strictly AFTER completed releases: they are already reflected.
	*now = now.Add(2 * time.Second)
	o.Generation++
	o.StartedNS = now.Add(-time.Second).UnixNano()
	o.CompletedNS = now.UnixNano()
	storageCreate(t, s, "next")
	if storageSpent(t, s) != cube.StorageGrant {
		t.Fatal("fresh observation failed to reset completed grants")
	}
}
func TestStorageCarryIncludesReleaseDuringObservationAndEqualBoundary(t *testing.T) {
	for _, delta := range []time.Duration{0, time.Nanosecond} {
		t.Run(delta.String(), func(t *testing.T) {
			s := openTestStore(t)
			o, now := storageSetup(t, s)
			a := storageCreate(t, s, "a")
			// Observer starts now, release can complete before file publication/read.
			start := now.UnixNano()
			*now = now.Add(delta)
			storagePause(t, s, a)
			*now = now.Add(time.Second)
			o.Generation++
			o.StartedNS = start
			o.CompletedNS = now.UnixNano()
			storageCreate(t, s, "b")
			if storageSpent(t, s) != 2*cube.StorageGrant {
				t.Fatal("released-after-start grant lost at epoch reset")
			}
		})
	}
}
func TestStorageConcurrentAdmissionsAndUncertainCarry(t *testing.T) {
	s := openTestStore(t)
	o, now := storageSetup(t, s)
	var granted atomic.Int32
	var wg sync.WaitGroup
	// Connect pre-existing released rows avoids the intentional serial create gate.
	for i := 0; i < 12; i++ {
		_, e := s.db.Exec(`INSERT INTO cube_admission(admission_key,runtime_id,template_id,operation,token,state,charged) VALUES(?,?,'tpl','pause',?,'released',0)`, fmt.Sprint(i), fmt.Sprint(i), fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.AdmissionBegin(context.Background(), fmt.Sprint(i), fmt.Sprint(i), "tpl", "connect", fmt.Sprint(i))
			if e == nil {
				granted.Add(1)
			} else if !errors.Is(e, cube.ErrCapacityUnavailable) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if granted.Load() != 4 || storageSpent(t, s) != 4*cube.StorageGrant {
		t.Fatal("concurrent oversubscription")
	}
	// Pending outcomes remain charged/carry, even through a fresh observation.
	*now = now.Add(2 * time.Second)
	o.Generation++
	o.StartedNS = now.Add(-time.Second).UnixNano()
	o.CompletedNS = now.UnixNano()
	e := s.submit(context.Background(), func(db *sql.DB) error {
		tx, e := db.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		return s.storageAdmit(context.Background(), tx, "recovery", "replacement", true)
	})
	if !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatalf("uncertain carry admitted a fifth disk: %v", e)
	}
}
func TestStorageGuardRejectsReplayStaleClockAndIdentity(t *testing.T) {
	for _, kind := range []string{"stale", "future", "clock_backwards", "replay", "changed_same_epoch", "wrong_boot", "wrong_uuid", "inner_low", "outer_low"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			o, now := storageSetup(t, s)
			storagePause(t, s, storageCreate(t, s, "first"))
			switch kind {
			case "stale":
				*now = now.Add(31 * time.Second)
			case "future":
				o.CompletedNS = now.Add(time.Nanosecond).UnixNano()
			case "clock_backwards":
				*now = now.Add(-time.Nanosecond)
			case "replay":
				o.Generation = 0
			case "changed_same_epoch":
				o.InnerFreeBytes++
			case "wrong_boot":
				o.WorkerBootID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			case "wrong_uuid":
				o.InnerFSUUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			case "inner_low":
				o.InnerFreeBytes = cube.StorageBaseline - 1
			case "outer_low":
				o.OuterFreeBytes = cube.StorageBaseline - 1
			}
			if _, e := s.AdmissionBegin(context.Background(), "denied", "", "tpl", "create", "denied"); !errors.Is(e, cube.ErrStorageUnavailable) {
				t.Fatal(e)
			}
			var n int
			s.db.QueryRow(`SELECT COUNT(*) FROM cube_admission WHERE admission_key='denied'`).Scan(&n)
			if n != 0 {
				t.Fatal("denial reserved CPU")
			}
		})
	}
}
func TestStorageEpochSurvivesRestartAndCannotBeDisabled(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "restart.db") + "?_journal=WAL"
	s, e := Open(ctx, dsn, "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	storageSetup(t, s)
	for i := 0; i < 4; i++ {
		storagePause(t, s, storageCreate(t, s, fmt.Sprint(i)))
	}
	s.Close()
	s, e = Open(ctx, dsn, "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.ConfigureStorageGuard(ctx, nil); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("guard disabled", e)
	}
	if _, e = s.AdmissionBegin(ctx, "unguarded", "", "tpl", "create", "unguarded"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("fresh adapter bypassed enrolled policy", e)
	}
	storageSetup(t, s)
	if _, e = s.AdmissionBegin(ctx, "new", "", "tpl", "create", "new"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("restart reset epoch", e)
	}
}
func TestStorageReleaseCASAndClockRollbackRemainConservative(t *testing.T) {
	s := openTestStore(t)
	_, now := storageSetup(t, s)
	a := storageCreate(t, s, "a")
	stale := a
	stale.Token = "wrong"
	if e := s.AdmissionObserveReleased(context.Background(), stale, true); e != nil {
		t.Fatal(e)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM cube_storage_grant WHERE released_ns IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatal("stale observation released current grant")
	}
	*now = now.Add(-time.Second)
	storagePause(t, s, a)
	s.db.QueryRow(`SELECT COUNT(*) FROM cube_storage_grant WHERE released_ns IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatal("backwards release timestamp undercounted carry")
	}
}

func TestStorageBootPinsRequireExplicitReviewAndCarryUnresolvedGrants(t *testing.T) {
	s := openTestStore(t)
	o, now := storageSetup(t, s)
	ctx := context.Background()
	a := storageCreate(t, s, "released-old")
	storagePause(t, s, a)
	storageCreate(t, s, "uncertain-old")
	cfg := storageConfig()
	newBoot := "77777777-7777-7777-7777-777777777777"
	o.OuterBootID = newBoot
	o.Generation++
	o.StartedNS = int64(time.Second)
	o.CompletedNS = int64(2 * time.Second)
	s.storageNow = func() (cube.StorageClock, error) {
		return cube.StorageClock{BootID: newBoot, NS: int64(2 * time.Second)}, nil
	}
	if _, e := s.AdmissionBegin(ctx, "denied", "", "tpl", "create", "denied"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("new file/boot changed durable policy without config review", e)
	}
	cfg.OuterBootID = newBoot
	if e := s.ConfigureStorageGuard(ctx, &cfg); e != nil {
		t.Fatal(e)
	}
	storageCreate(t, s, "after-review")
	if storageSpent(t, s) != 2*cube.StorageGrant {
		t.Fatal("old unresolved grant lost or prior-boot completed release incorrectly carried")
	}
	_ = now
}
func TestStorageRecoveryReplacementRequiresSeparateGrant(t *testing.T) {
	s, p := recoveryFixture(t, 4)
	ctx := context.Background()
	// Enroll only after authoritative releases; guarded reconnect creates real grants.
	for i := 0; i < 4; i++ {
		a, e := s.AdmissionLookup(ctx, fmt.Sprintf("old-%d", i))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.AdmissionObserveReleased(ctx, a, false); e != nil {
			t.Fatal(e)
		}
	}
	storageSetup(t, s)
	for i := 0; i < 4; i++ {
		a, e := s.AdmissionBegin(ctx, fmt.Sprintf("app:app-%d", i), fmt.Sprintf("old-%d", i), "tpl-reviewed", "connect", fmt.Sprintf("connect-%d", i))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.AdmissionFinish(ctx, a, a.RuntimeID, "active"); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.BeginCubeRecovery(ctx, p); e != nil {
		t.Fatal(e)
	}
	fenceRecovery(t, s)
	j := recoveryJournal(t, s)
	_, e := s.CubeRecoveryCreateIntent(ctx, j.ID, "replace", recoveryHash("request"), j.SupervisorSHA256, j.Target.TemplateID, j.SandboxID, j.AppID)
	if !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatalf("replacement reused old disk budget: %v", e)
	}
	j = recoveryJournal(t, s)
	if j.Phase != "fenced" || storageSpent(t, s) != 4*cube.StorageGrant {
		t.Fatal("denied recovery mutated journal/epoch")
	}
	assertRecoveryCharge(t, s, 4)
}

func TestStorageStaleObservationBlocksWakeButAllowsPauseAndRunningDelete(t *testing.T) {
	s := openTestStore(t)
	_, now := storageSetup(t, s)
	ctx := context.Background()
	a := storageCreate(t, s, "a")
	b := storageCreate(t, s, "b")
	*now = now.Add(31 * time.Second)
	storagePause(t, s, a)
	if _, e := s.AdmissionBegin(ctx, a.Key, a.RuntimeID, a.TemplateID, "connect", "wake"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("stale wake admitted", e)
	}
	d, e := s.AdmissionBegin(ctx, b.Key, b.RuntimeID, b.TemplateID, "delete", "delete")
	if e != nil {
		t.Fatal("known charged running delete blocked on observer", e)
	}
	if e = s.AdmissionFinish(ctx, d, b.RuntimeID, "deleted"); e != nil {
		t.Fatal(e)
	}
	// A paused Delete can cause an upstream resume, so requires fresh disk grant.
	if _, e = s.AdmissionBegin(ctx, a.Key, a.RuntimeID, a.TemplateID, "delete", "paused-delete"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("paused delete bypassed observation", e)
	}
}

func BenchmarkCubeStorageAdmission(b *testing.B) {
	for _, guarded := range []bool{false, true} {
		b.Run(fmt.Sprint(guarded), func(b *testing.B) {
			ctx := context.Background()
			s, e := Open(ctx, "file:"+filepath.Join(b.TempDir(), "state.db")+"?_journal=WAL", "../../migrations")
			if e != nil {
				b.Fatal(e)
			}
			defer s.Close()
			if e = s.AdmissionPolicy(ctx, 4, "cpu=2;memory_mb=2048"); e != nil {
				b.Fatal(e)
			}
			if guarded {
				if os.Geteuid() != 0 {
					b.Skip("root-owned observer file required")
				}
				clock, e := cube.ReadStorageClock()
				if e != nil {
					b.Skip("Linux clock required")
				}
				cfg := storageConfig()
				cfg.OuterBootID = clock.BootID
				cfg.ObservationPath = filepath.Join(b.TempDir(), "observation.json")
				o := observation(cfg, time.Unix(0, clock.NS))
				o.StartedNS = clock.NS
				o.CompletedNS = clock.NS
				o.InnerFreeBytes = 1 << 50
				o.OuterFreeBytes = 1 << 50
				raw, _ := json.Marshal(o)
				if e = os.WriteFile(cfg.ObservationPath, raw, 0600); e != nil {
					b.Fatal(e)
				}
				if e = s.ConfigureStorageGuard(ctx, &cfg); e != nil {
					b.Fatal(e)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := fmt.Sprint(i)
				a, e := s.AdmissionBegin(ctx, id, "", "tpl", "create", "create-"+id)
				if e != nil {
					b.Fatal(e)
				}
				if e = s.AdmissionFinish(ctx, a, "vm-"+id, "active"); e != nil {
					b.Fatal(e)
				}
				a, e = s.AdmissionBegin(ctx, id, "vm-"+id, "tpl", "pause", "pause-"+id)
				if e != nil {
					b.Fatal(e)
				}
				if e = s.AdmissionFinish(ctx, a, a.RuntimeID, "released"); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}

func TestStorageRefusalDoesNotPOSTAndUnavailableRuntimeRetainsGrant(t *testing.T) {
	s := openTestStore(t)
	_, now := storageSetup(t, s)
	ctx := context.Background()
	cfg := storageConfig()
	var posts atomic.Int32
	var unavailable atomic.Bool
	var metadata map[string]string
	var metadataMu sync.Mutex
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataMu.Lock()
		defer metadataMu.Unlock()
		if r.Method == "POST" {
			var input cube.CreateRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				t.Error("invalid create fixture request")
			}
			metadata = input.Metadata
			posts.Add(1)
			w.WriteHeader(201)
		}
		state := "running"
		if unavailable.Load() {
			state = "unknown"
		}
		json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-fixture", TemplateID: "tpl-reviewed", State: state, CPUCount: 2, MemoryMB: 2048, Metadata: metadata})
	}))
	defer provider.Close()
	c, e := cube.New(cube.Config{APIURL: provider.URL, APIKey: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	a := admissionConfig(4)
	a.StorageGuard = &cfg
	a.WritableDiskMB = 10240
	if e = c.ConfigureAdmission(ctx, s, a); e != nil {
		t.Fatal(e)
	}
	*now = now.Add(31 * time.Second)
	if _, e = c.Create(ctx, admissionInput("denied")); !errors.Is(e, cube.ErrStorageUnavailable) || posts.Load() != 0 {
		t.Fatal("stale guard sent allocation POST", e)
	}
	*now = now.Add(-31 * time.Second)
	if _, e = c.Create(ctx, admissionInput("allowed")); e != nil {
		t.Fatal(e)
	}
	unavailable.Store(true)
	if _, e = c.Connect(ctx, "vm-fixture", cube.ConnectRequest{}); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		t.Fatal(e)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM cube_storage_grant WHERE released_ns IS NULL`).Scan(&n)
	if n != 1 || posts.Load() != 1 {
		t.Fatal("unavailable runtime freed grant or retried")
	}
	assertRecoveryCharge(t, s, 1)
}

func TestStorageFailurePrecedesCPUReclamationSignal(t *testing.T) {
	s := openTestStore(t)
	_, now := storageSetup(t, s)
	for i := 0; i < 4; i++ {
		storageCreate(t, s, fmt.Sprint(i))
	}
	*now = now.Add(31 * time.Second)
	_, e := s.AdmissionBegin(context.Background(), "fifth", "", "tpl", "create", "fifth")
	if !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatalf("stale disk reported as evictable CPU exhaustion: %v", e)
	}
	assertRecoveryCharge(t, s, 4)
}

func TestStorageLostObserverSequenceCannotResetEnrolledEpoch(t *testing.T) {
	s := openTestStore(t)
	o, now := storageSetup(t, s)
	o.Generation = 82
	storagePause(t, s, storageCreate(t, s, "first"))
	*now = now.Add(time.Second)
	o.Generation = 1
	o.StartedNS = now.UnixNano()
	o.CompletedNS = now.UnixNano()
	if _, e := s.AdmissionBegin(context.Background(), "after-reset", "", "tpl", "create", "after-reset"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatal("missing observer sequence reset durable epoch", e)
	}
	if storageSpent(t, s) != cube.StorageGrant {
		t.Fatal("failed observation reset debit")
	}
}
