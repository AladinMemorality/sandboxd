package cube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInventoryRequiresCompleteUnfilteredIdentitySet(t *testing.T) {
	for _, kind := range []string{"empty", "all-states", "fleet", "null", "duplicate", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v2/sandboxes" || r.URL.RawQuery != "limit=4096" {
					t.Error("inventory was filtered or mutated provider")
				}
				var value any
				switch kind {
				case "empty":
					value = []Sandbox{}
				case "null":
					value = nil
				case "all-states":
					value = []Sandbox{{SandboxID: "a", State: "running"}, {SandboxID: "b", State: "paused"}, {SandboxID: "c", State: "stopped"}}
				case "duplicate":
					value = []Sandbox{{SandboxID: "a"}, {SandboxID: "a"}}
				case "fleet", "truncated":
					rows := []Sandbox{}
					count := inventoryLimit
					if kind == "fleet" {
						count = 202
					}
					for i := 0; i < count; i++ {
						rows = append(rows, Sandbox{SandboxID: fmt.Sprintf("vm-%d", i)})
					}
					value = rows
				}
				json.NewEncoder(w).Encode(value)
			}))
			defer provider.Close()
			client, e := New(Config{APIURL: provider.URL, APIKey: "fixture"})
			if e != nil {
				t.Fatal(e)
			}
			out, e := client.Inventory(context.Background())
			valid := kind == "empty" || kind == "all-states" || kind == "fleet"
			if valid && e != nil {
				t.Fatal(e)
			}
			if !valid && e == nil {
				t.Fatal("incomplete inventory accepted")
			}
			if kind == "all-states" && len(out) != 3 {
				t.Fatal("stopped identity was omitted")
			}
			if kind == "fleet" && len(out) != 202 {
				t.Fatal("fleet inventory was truncated")
			}
		})
	}
}

func TestNodeInventoryNeverEnumeratesAnotherWorker(t *testing.T) {
	calls := 0
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/cube/sandbox/inventory" || r.URL.Query().Get("host_id") != "10.0.2.15" {
			t.Error("unexpected remote/fleet access")
			http.Error(w, "bad", 500)
			return
		}
		fmt.Fprint(w, `{"ret":{"ret_code":200},"data":[{"sandbox_id":"local","host_id":"10.0.2.15","status":1,"cpu_count":1,"memory_mb":512}]}`)
	}))
	defer master.Close()
	c, e := New(Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ConfigurePlacement(master.URL, "cubebox"); e != nil {
		t.Fatal(e)
	}
	out, e := c.InventoryOnNode(context.Background(), "10.0.2.15")
	if e != nil || len(out) != 1 || out[0].CPUCount != 1 || calls != 1 {
		t.Fatal(out, e, calls)
	}
}

func TestNodeInventoryRejectsIncompleteOrForeignResults(t *testing.T) {
	for _, body := range []string{`{"ret":{"ret_code":200}}`, `{"ret":{"ret_code":500},"data":[]}`, `{"ret":{"ret_code":200},"data":[{"sandbox_id":"foreign","host_id":"b200"}]}`, `{"ret":{"ret_code":200},"data":[{"sandbox_id":"one","host_id":"vps"},{"sandbox_id":"one","host_id":"vps"}]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		c, _ := New(Config{APIURL: server.URL, APIKey: "fixture"})
		c.ConfigurePlacement(server.URL, "cubebox")
		if _, e := c.InventoryOnNode(context.Background(), "vps"); e == nil {
			t.Fatal("unsafe inventory accepted", body)
		}
		server.Close()
	}
}

func TestReadinessDoesNotContactDrainingWorker(t *testing.T) {
	localCalls := 0
	local := &Client{admission: &admissionGuard{config: AdmissionConfig{NodeID: "local"}}, inventoryNode: func(ctx context.Context, node string) ([]Sandbox, error) { localCalls++; return []Sandbox{}, nil }}
	remote := &Client{admission: &admissionGuard{config: AdmissionConfig{NodeID: "remote"}}, inventoryNode: func(ctx context.Context, node string) ([]Sandbox, error) {
		t.Fatal("draining worker contacted")
		return nil, nil
	}}
	client := &Client{fleet: &fleet{workers: map[string]*Client{"vps": local, "b200": remote}, order: []string{"vps"}}}
	if err := client.CheckReadiness(context.Background()); err != nil || localCalls != 1 {
		t.Fatal(err, localCalls)
	}
	local.inventoryNode = func(context.Context, string) ([]Sandbox, error) { return nil, fmt.Errorf("VPS unavailable") }
	if err := client.CheckReadiness(context.Background()); err == nil {
		t.Fatal("local outage hidden")
	}
	client.fleet.order = nil
	if err := client.CheckReadiness(context.Background()); err == nil {
		t.Fatal("empty allocation fleet is ready")
	}
}
