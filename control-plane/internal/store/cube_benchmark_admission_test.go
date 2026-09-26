package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// Exercises actual SQLite policy enrollment + finite storage reservations. It
// creates no guest, requires no remote endpoint, and runs in both build modes.
func TestCompiledBenchmarkStorageEnrollmentAndCapacity(t *testing.T) {
	for _, slots := range []int{4, 6, 8, 12} {
		for _, r := range []cube.AdmissionResources{{CPUCount: 1, MemoryMB: 1024}, {CPUCount: 1, MemoryMB: 2048}, {CPUCount: 2, MemoryMB: 2048}} {
			t.Run(fmt.Sprintf("%d/%d/%d", slots, r.CPUCount, r.MemoryMB), func(t *testing.T) {
				s := openTestStore(t)
				ctx := context.Background()
				guard := storageConfig()
				now := time.Unix(1800000000, 0)
				o := observation(guard, now)
				o.InnerFreeBytes, o.OuterFreeBytes = 512*cube.StorageGiB, 512*cube.StorageGiB
				s.storageNow = func() (cube.StorageClock, error) {
					return cube.StorageClock{BootID: guard.OuterBootID, NS: now.UnixNano()}, nil
				}
				s.storageRead = func(cube.StorageGuardConfig, cube.StorageClock) (cube.StorageObservation, error) { return o, nil }
				c, err := cube.New(cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "not-used"})
				if err != nil {
					t.Fatal(err)
				}
				cfg := cube.AdmissionConfig{MaxActive: slots, CPUCount: r.CPUCount, MemoryMB: r.MemoryMB, WritableDiskMB: 10240, StorageGuard: &guard, Templates: map[string]cube.AdmissionResources{"tpl-reviewed": r}}
				err = c.ConfigureAdmission(ctx, s, cfg)
				allowed := cube.BenchmarkAdmissionBuild || (slots == 4 && r.CPUCount == 2 && r.MemoryMB == 2048)
				if !allowed {
					if err == nil {
						t.Fatal("production admitted benchmark profile")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if s.AdmissionPolicy(ctx, slots+1, fmt.Sprintf("cpu=%d;memory_mb=%d", r.CPUCount, r.MemoryMB)) == nil {
					t.Fatal("durable cap changed")
				}
				for i := 0; i < slots; i++ {
					storageCreate(t, s, fmt.Sprintf("bench-%d", i))
				}
				if got := storageSpent(t, s); got != int64(slots)*cube.StorageGrant {
					t.Fatalf("storage debit %d", got)
				}
				if _, err = s.AdmissionBegin(ctx, "overflow", "", "tpl", "create", "overflow"); !errors.Is(err, cube.ErrAdmissionCapacity) {
					t.Fatalf("overflow: %v", err)
				}
				if got := storageSpent(t, s); got != int64(slots)*cube.StorageGrant {
					t.Fatal("failed transaction consumed storage")
				}
				if err = s.ConfigureStorageGuard(ctx, nil); !errors.Is(err, cube.ErrStorageUnavailable) {
					t.Fatalf("disabled enrolled guard: %v", err)
				}
				now = now.Add(31 * time.Second)
				if _, err = s.AdmissionBegin(ctx, "stale", "", "tpl", "create", "stale"); !errors.Is(err, cube.ErrStorageUnavailable) {
					t.Fatalf("stale observation: %v", err)
				}
			})
		}
	}
}
