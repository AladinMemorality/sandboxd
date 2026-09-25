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
	for _, kind := range []string{"empty", "all-states", "null", "duplicate", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/sandboxes" || r.URL.RawQuery != "" {
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
				case "truncated":
					rows := []Sandbox{}
					for i := 0; i < 200; i++ {
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
			valid := kind == "empty" || kind == "all-states"
			if valid && e != nil {
				t.Fatal(e)
			}
			if !valid && e == nil {
				t.Fatal("incomplete inventory accepted")
			}
			if kind == "all-states" && len(out) != 3 {
				t.Fatal("stopped identity was omitted")
			}
		})
	}
}
