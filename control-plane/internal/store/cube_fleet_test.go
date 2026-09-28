package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// Exercise the public provider client and the real durable ledger together.
// This is a protocol test, not evidence that live microVMs were started.
func TestCubeFleetRoutesFiftyAndKeepsUncertainPlacementCharged(t *testing.T) {
	testCubeFleetCapacity(t, 50)
}
func TestCubeFleetRoutesHundredAndKeepsUncertainPlacementCharged(t *testing.T) {
	testCubeFleetCapacity(t, 100)
}
func testCubeFleetCapacity(t *testing.T, total int) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(1800000000, 0)
	s.storageNow = func() (cube.StorageClock, error) {
		return cube.StorageClock{BootID: storageConfig().OuterBootID, NS: now.UnixNano()}, nil
	}
	s.storageRead = func(cfg cube.StorageGuardConfig, _ cube.StorageClock) (cube.StorageObservation, error) {
		o := observation(cfg, now)
		o.InnerFreeBytes = 2048 * cube.StorageGiB
		o.OuterFreeBytes = 4096 * cube.StorageGiB
		return o, nil
	}
	var mu sync.Mutex
	vms := map[string]*cube.Sandbox{}
	nodes := map[string]string{}
	counts := map[string]int{}
	wrongPlacement := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/cube/sandbox/info" {
			id := r.URL.Query().Get("sandbox_id")
			vm:=vms[id];state:=1;if vm.State=="paused"{state=5}
            json.NewEncoder(w).Encode(map[string]any{"ret":map[string]int{"ret_code":200},"data":[]any{map[string]any{"sandbox_id":id,"host_id":nodes[id],"template_id":vm.TemplateID,"status":state,"labels":vm.Metadata,"containers":[]any{map[string]any{"container_id":id,"cpu_milli":vm.CPUCount*1000,"memory_mib":vm.MemoryMB}}}}})
			return
		}
        if r.URL.Path=="/cube/sandbox/update" {
            var in struct{ID string `json:"sandbox_id"`;Action string `json:"action"`};json.NewDecoder(r.Body).Decode(&in)
            if in.Action!="resume"||vms[in.ID]==nil{w.WriteHeader(400);return}
            vms[in.ID].State="running";json.NewEncoder(w).Encode(map[string]any{"ret":map[string]int{"ret_code":200}});return
        }
		if r.URL.Path == "/sandboxes" && r.Method == "POST" {
			var in cube.CreateRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.DistributionScope) != 1 {
				w.WriteHeader(400)
				return
			}
			id := "vm-" + in.Metadata["sandboxd_id"]
			vms[id] = &cube.Sandbox{SandboxID: id, TemplateID: in.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: in.Metadata}
			nodes[id] = in.DistributionScope[0]
			counts[nodes[id]]++
			if wrongPlacement {
				nodes[id] = "wrong-node"
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(vms[id])
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 || vms[parts[1]] == nil {
			w.WriteHeader(404)
			return
		}
		vm := vms[parts[1]]
		if r.Method == "DELETE" {
			delete(vms, vm.SandboxID)
			w.WriteHeader(204)
			return
		}
		if len(parts) == 3 && parts[2] == "pause" {
			vm.State = "paused"
			w.WriteHeader(204)
			return
		}
		if len(parts) == 3 && parts[2] == "connect" {
			vm.State = "running"
		}
		json.NewEncoder(w).Encode(vm)
	}))
	defer provider.Close()
	c, err := cube.New(cube.Config{APIURL: provider.URL, APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ConfigurePlacement(provider.URL, "cubebox"); err != nil {
		t.Fatal(err)
	}
	var workers []cube.FleetWorkerConfig
	for _, entry := range []struct {
		name  string
		slots int
	}{{"vps", 4}, {"b200", total - 4}} {
		cfg := admissionConfig(entry.slots)
		cfg.NodeID = "node-" + entry.name
		cfg.HostCPUMillis = entry.slots * 2300
		cfg.HostMemoryMB = entry.slots * 2160
		cfg.WritableDiskMB = 10240
		guard := storageConfig()
		guard.ObservationPath = "/run/" + entry.name + "/observation.json"
		cfg.StorageGuard = &guard
		workers = append(workers, cube.FleetWorkerConfig{ID: entry.name, ProxyURL: "http://" + entry.name + ".invalid", Admission: cfg})
	}
	if err = c.ConfigureFleet(ctx, s, workers); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		if _, err = c.Create(ctx, admissionInput(fmt.Sprint(i))); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if counts["node-vps"] != 4 || counts["node-b200"] != total-4 {
		t.Fatalf("wrong placement counts: %v", counts)
	}
	if _, err = c.Create(ctx, admissionInput("overflow")); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("admission %d: %v", total+1, err)
	}
	for _, entry := range []struct{ id, origin string }{{"vm-0", "http://vps.invalid"}, {fmt.Sprintf("vm-%d", total-1), "http://b200.invalid"}} {
		origin, err := c.ProxyOrigin(ctx, entry.id, "http://fallback.invalid")
		if err != nil || origin != entry.origin {
			t.Fatalf("proxy origin: %s %v", origin, err)
		}
		if _, err = c.Connect(ctx, entry.id, cube.ConnectRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.Pause(ctx, fmt.Sprintf("vm-%d", total-1)); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Connect(ctx, fmt.Sprintf("vm-%d", total-1), cube.ConnectRequest{}); err != nil {
		t.Fatal(err)
	}
	if err = c.Delete(ctx, fmt.Sprintf("vm-%d", total-1)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	wrongPlacement = true
	mu.Unlock()
	if _, err = c.Create(ctx, admissionInput("wrong")); !errors.Is(err, cube.ErrAdmissionPending) {
		t.Fatalf("wrong worker acknowledged: %v", err)
	}
	a, err := s.AdmissionLookupKey(ctx, "app:app-wrong")
	if err != nil || a.Charged != 1 || a.State != "pending" || a.WorkerID != "b200" {
		t.Fatalf("uncertain charge lost: %+v %v", a, err)
	}
	var used int
	if err = s.db.QueryRow(`SELECT SUM(charged) FROM cube_admission`).Scan(&used); err != nil || used != total {
		t.Fatalf("final charges %d: %v", used, err)
	}
}
