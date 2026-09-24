package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func TestLatestEvidenceRequiresAllThreeStores(t *testing.T) {
	latest := marker{"fixture", "latest", "new"}
	data := map[string]any{"matches": true, "app": latest, "home": latest, "database": latest}
	b, _ := json.Marshal(data)
	if validateEvidence(b, latest) != nil {
		t.Fatal("exact evidence rejected")
	}
	for _, field := range []string{"app", "home", "database"} {
		copy := map[string]any{}
		for k, v := range data {
			copy[k] = v
		}
		copy[field] = marker{"fixture", "baseline", "old"}
		b, _ = json.Marshal(copy)
		if validateEvidence(b, latest) == nil {
			t.Fatalf("stale %s passed", field)
		}
	}
	if validateEvidence([]byte(`{"matches":true}`), latest) == nil {
		t.Fatal("health-only passed")
	}
}
func TestEvidenceCreationRefusesOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "escrow.private.json")
	if e := durable(p, []byte("original"), true); e != nil {
		t.Fatal(e)
	}
	if e := durable(p, []byte("replacement"), true); e == nil {
		t.Fatal("overwrote prior escrow")
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != "original" {
		t.Fatal("prior escrow changed")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("escrow not private")
	}
}
func TestCleanupRequiresExactOwnershipThenGET404(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "foreign"}[foreign], func(t *testing.T) {
			var deleted atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deleted.Store(true)
					w.WriteHeader(204)
					return
				}
				if deleted.Load() {
					w.WriteHeader(404)
					return
				}
				owner := "synthetic"
				if foreign {
					owner = "other"
				}
				json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-crash-test", TemplateID: template, CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"operator-crash-fixture": owner}})
			}))
			defer server.Close()
			api, e := cube.New(cube.Config{APIURL: server.URL, APIKey: "synthetic-only"})
			if e != nil {
				t.Fatal(e)
			}
			c := &coordinator{cube: api, saved: escrow{Fixture: "synthetic", Guest: &cube.Sandbox{SandboxID: "vm-crash-test"}}}
			if got := c.cleanup(); got == foreign {
				t.Fatalf("cleanup=%v foreign=%v", got, foreign)
			}
			if deleted.Load() == foreign {
				t.Fatal("cleanup deleted foreign guest or missed owned guest")
			}
		})
	}
}
func TestInventoryRequiresOneExactZero(t *testing.T) {
	if !emptyInventory("SANDBOX_COUNT    0\n") {
		t.Fatal("empty rejected")
	}
	for _, s := range []string{"", "SANDBOX_COUNT    1", "SANDBOX_COUNT    0\nSANDBOX_COUNT    1", "SANDBOX_COUNT    00"} {
		if emptyInventory(s) {
			t.Fatal("nonempty/ambiguous accepted")
		}
	}
}
