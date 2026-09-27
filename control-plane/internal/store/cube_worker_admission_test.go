package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"sync"
	"testing"
)

func TestCubeWorkerAdmissionFiftyIndependentCharges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	vps, err := s.AdmissionPartition("vps")
	if err != nil {
		t.Fatal(err)
	}
	b200, err := s.AdmissionPartition("b200")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		db    cube.AdmissionStore
		slots int
		name  string
	}{{vps, 4, "vps"}, {b200, 46, "b200"}} {
		if err := w.db.AdmissionPolicy(ctx, w.slots, "cpu=2;memory_mb=2048"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < w.slots; i++ {
			key := fmt.Sprintf("app:%s-%d", w.name, i)
			a, err := w.db.AdmissionBegin(ctx, key, "", "tpl", "create", key)
			if err != nil {
				t.Fatal(err)
			}
			if a.WorkerID != w.name {
				t.Fatalf("wrong worker: %+v", a)
			}
			if err = w.db.AdmissionFinish(ctx, a, fmt.Sprintf("vm-%s-%d", w.name, i), "active"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.db.AdmissionBegin(ctx, "app:overflow-"+w.name, "", "tpl", "create", "overflow"); !errors.Is(err, cube.ErrCapacityUnavailable) {
			t.Fatalf("overflow: %v", err)
		}
	}
	var charged int
	if err = s.db.QueryRow(`SELECT SUM(charged) FROM cube_admission`).Scan(&charged); err != nil || charged != 50 {
		t.Fatalf("charges=%d err=%v", charged, err)
	}
	a, err := b200.AdmissionLookup(ctx, "vm-b200-0")
	if err != nil {
		t.Fatal(err)
	}
	if err = vps.AdmissionObserveReleased(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	current, err := b200.AdmissionLookup(ctx, "vm-b200-0")
	if err != nil || current.Charged != 1 {
		t.Fatalf("foreign release changed charge: %+v %v", current, err)
	}
	if err = b200.AdmissionObserveReleased(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if _, err = b200.AdmissionBegin(ctx, "app:b200-replacement", "", "tpl", "create", "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err = vps.AdmissionBegin(ctx, "app:vps-still-full", "", "tpl", "create", "full"); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("B200 release altered VPS capacity: %v", err)
	}
}

func TestCubeWorkerAdmissionSameProjectCannotRaceAcrossWorkers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"vps", "b200"} {
		partition, err := s.AdmissionPartition(name)
		if err != nil {
			t.Fatal(err)
		}
		if err = partition.AdmissionPolicy(ctx, 4, "cpu=2;memory_mb=2048"); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(db cube.AdmissionStore, token string) {
			defer wg.Done()
			_, err := db.AdmissionBegin(ctx, "app:same", "", "tpl", "create", token)
			results <- err
		}(partition, name)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, cube.ErrAdmissionPending) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatalf("same project reserved on %d workers", accepted)
	}
}
