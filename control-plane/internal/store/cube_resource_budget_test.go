package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func resourceTestConfig(memory int) cube.AdmissionConfig {
	return cube.AdmissionConfig{NodeID: "10.0.2.15", MaxActive: 12, HostCPUMillis: 20000, HostMemoryMB: 24000,
		Templates: map[string]cube.AdmissionResources{"small": {CPUCount: 1, MemoryMB: 512}, "large": {CPUCount: 1, MemoryMB: 1024}, "builder": {CPUCount: 2, MemoryMB: 3072}},
		ResourceBudget: &cube.ResourceBudget{CPUMillis: 10000, MemoryMB: memory, RuntimeSlots: 10, BuildSlots: 2, Profiles: map[string]cube.ResourceProfile{
			"small": {CPUMillis: 100, WritableDiskMB: 4096, Kind: "runtime"}, "large": {CPUMillis: 200, WritableDiskMB: 8192, Kind: "runtime"}, "builder": {CPUMillis: 2000, WritableDiskMB: 8192, Kind: "build"},
		}},
	}
}

func enrollBudget(t *testing.T, s *Store, cfg cube.AdmissionConfig) {
	t.Helper()
	ctx := context.Background()
	if e := s.AdmissionPolicy(ctx, cfg.MaxActive, "resource-budget-v1"); e != nil {
		t.Fatal(e)
	}
	if e := s.ConfigureResourceBudget(ctx, cfg); e != nil {
		t.Fatal(e)
	}
}

func seedPausedBudgetRuntime(t *testing.T, s *Store, id, template string) {
	t.Helper()
	if _, e := s.db.Exec(`INSERT INTO cube_admission(admission_key,runtime_id,template_id,operation,token,state,charged,worker_id) VALUES(?,?,?,'pause',?,'released',0,'vps')`, "app:"+id, id, template, "old-"+id); e != nil {
		t.Fatal(e)
	}
}

func TestResourceBudgetConcurrentStartsCannotOverbookMemory(t *testing.T) {
	s := openTestStore(t)
	cfg := resourceTestConfig(3200)
	enrollBudget(t, s, cfg)
	// Full configured memory plus 128MiB overhead: exactly five small guests.
	for i := 0; i < 20; i++ {
		seedPausedBudgetRuntime(t, s, fmt.Sprint(i), "small")
	}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			_, e := s.AdmissionBegin(context.Background(), "app:"+id, id, "small", "connect", "new-"+id)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for e := range results {
		if e == nil {
			accepted++
		} else if !errors.Is(e, cube.ErrCapacityUnavailable) {
			t.Fatal(e)
		}
	}
	if accepted != 5 {
		t.Fatalf("accepted %d instead of 5", accepted)
	}
	// Pending operations retain capacity until an authoritative completion.
	if _, e := s.AdmissionBegin(context.Background(), "app:other", "", "small", "create", "other"); !errors.Is(e, cube.ErrCapacityUnavailable) {
		t.Fatalf("pending charges lost: %v", e)
	}
}

func TestResourceBudgetBuildQueueDoesNotConsumeRuntimeSlots(t *testing.T) {
	s := openTestStore(t)
	cfg := resourceTestConfig(20000)
	enrollBudget(t, s, cfg)
	for i := 0; i < 3; i++ {
		id := fmt.Sprint(i)
		seedPausedBudgetRuntime(t, s, id, "builder")
		_, e := s.AdmissionBegin(context.Background(), "app:"+id, id, "builder", "connect", "new-"+id)
		if i < 2 && e != nil {
			t.Fatal(e)
		}
		if i == 2 && !errors.Is(e, cube.ErrCapacityUnavailable) {
			t.Fatalf("third build admitted: %v", e)
		}
	}
	seedPausedBudgetRuntime(t, s, "serving", "small")
	if _, e := s.AdmissionBegin(context.Background(), "app:serving", "serving", "small", "connect", "serving"); e != nil {
		t.Fatal(e)
	}
}

func TestResourceBudgetPersistsAndCannotBeRaisedOnRestart(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "state.db") + "?_fk=1"
	s, e := Open(ctx, dsn, "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	cfg := resourceTestConfig(3200)
	enrollBudget(t, s, cfg)
	seedPausedBudgetRuntime(t, s, "held", "builder")
	if _, e = s.AdmissionBegin(ctx, "app:held", "held", "builder", "connect", "held"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, dsn, "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	enrollBudget(t, s, cfg)
	if _, e = s.AdmissionBegin(ctx, "app:new", "", "small", "create", "new"); !errors.Is(e, cube.ErrCapacityUnavailable) {
		t.Fatalf("restart lost pending memory: %v", e)
	}
	cfg.ResourceBudget.MemoryMB = 6400
	if s.ConfigureResourceBudget(ctx, cfg) == nil {
		t.Fatal("silently raised immutable budget")
	}
	cfg.ResourceBudget = nil
	if s.ConfigureResourceBudget(ctx, cfg) == nil {
		t.Fatal("disabled durable budget")
	}
}

func TestResourceBudgetRejectsUnknownTemplateAndPricesStorage(t *testing.T) {
	s := openTestStore(t)
	enrollBudget(t, s, resourceTestConfig(20000))
	ctx := context.Background()
	if _, e := s.AdmissionBegin(ctx, "app:unknown", "", "unknown", "create", "unknown"); !errors.Is(e, cube.ErrAdmissionUnknown) {
		t.Fatalf("unknown accepted: %v", e)
	}
	if e := s.submit(ctx, func(db *sql.DB) error {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for id, want := range map[string]int64{"small": (4096 + 512) * 1024 * 1024, "large": (8192 + 1024) * 1024 * 1024, "builder": (8192 + 3072) * 1024 * 1024} {
			got, e := s.storageGrantBytes(ctx, tx, id)
			if e != nil || got != want {
				return fmt.Errorf("%s grant=%d want=%d err=%v", id, got, want, e)
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestResourceBudgetStorageGrantsAndEpoch(t *testing.T) {
	s := openTestStore(t)
	enrollBudget(t, s, resourceTestConfig(20000))
	ctx := context.Background()
	cfg := storageConfig()
	now := time.Unix(1800000000, 0)
	o := observation(cfg, now)
	s.storageNow = func() (cube.StorageClock, error) {
		return cube.StorageClock{BootID: cfg.OuterBootID, NS: now.UnixNano()}, nil
	}
	s.storageRead = func(cube.StorageGuardConfig, cube.StorageClock) (cube.StorageObservation, error) { return o, nil }
	if err := s.ConfigureStorageGuard(ctx, &cfg); err != nil {
		t.Fatal(err)
	}
	// A release never refunds the current observation epoch. Ten 4.5GiB
	// reservations fit its 48GiB budget; the eleventh must be refused.
	for i := 0; i < 10; i++ {
		id := fmt.Sprint(i)
		a, e := s.AdmissionBegin(ctx, id, "", "small", "create", "create-"+id)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.AdmissionFinish(ctx, a, "vm-"+id, "active"); e != nil {
			t.Fatal(e)
		}
		a.RuntimeID = "vm-" + id
		storagePause(t, s, a)
	}
	if got := storageSpent(t, s); got != 45*1024*1024*1024 {
		t.Fatalf("spent %d", got)
	}
	if _, e := s.AdmissionBegin(ctx, "denied", "", "small", "create", "denied"); !errors.Is(e, cube.ErrStorageUnavailable) {
		t.Fatalf("epoch overbooking: %v", e)
	}
	now = now.Add(2 * time.Second)
	o.Generation++
	o.StartedNS = now.Add(-time.Second).UnixNano()
	o.CompletedNS = now.UnixNano()
	a, e := s.AdmissionBegin(ctx, "large", "", "large", "create", "large")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AdmissionFinish(ctx, a, "vm-large", "active"); e != nil {
		t.Fatal(e)
	}
	if got := storageSpent(t, s); got != 9*1024*1024*1024 {
		t.Fatalf("fresh epoch mixed profile grant %d", got)
	}
	var persisted int64
	if e = s.db.QueryRow(`SELECT bytes FROM cube_storage_grant WHERE token='large'`).Scan(&persisted); e != nil || persisted != 9*1024*1024*1024 {
		t.Fatal(persisted, e)
	}
}

func TestResourceBudgetMigrationPreservesLegacyGrants(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	entries, e := os.ReadDir("../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0040" {
			continue
		}
		data, e := os.ReadFile(filepath.Join("../../migrations", entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, entry.Name()), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	dsn := "file:" + filepath.Join(t.TempDir(), "legacy.db") + "?_fk=1"
	s, e := Open(ctx, dsn, dir)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`INSERT INTO cube_storage_grant(token,admission_key,granted_ns,grant_boot_id,released_ns,released_boot_id,bytes,worker_id) VALUES ('held','app:held',100,'boot',NULL,NULL,12884901888,'vps'),('released','app:released',50,'boot',75,'boot',12884901888,'b200')`)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, dsn, "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var held, released int
	if e = s.db.QueryRow(`SELECT COUNT(*) FROM cube_storage_grant WHERE token='held' AND admission_key='app:held' AND granted_ns=100 AND grant_boot_id='boot' AND released_ns IS NULL AND released_boot_id IS NULL AND bytes=12884901888 AND worker_id='vps'`).Scan(&held); e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(`SELECT COUNT(*) FROM cube_storage_grant WHERE token='released' AND admission_key='app:released' AND granted_ns=50 AND grant_boot_id='boot' AND released_ns=75 AND released_boot_id='boot' AND bytes=12884901888 AND worker_id='b200'`).Scan(&released); e != nil {
		t.Fatal(e)
	}
	if held != 1 || released != 1 {
		t.Fatal("legacy grants changed during migration")
	}
}
