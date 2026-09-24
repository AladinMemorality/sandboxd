package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func TestCubeUnavailableConnectKeepsReservationWithoutMutation(t *testing.T) {
	for _, state := range []string{"stopped", "unknown"} {
		t.Run(state, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			var mutations atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations.Add(1)
					w.WriteHeader(500)
					return
				}
				json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-owned", TemplateID: "tpl-reviewed", State: state, CPUCount: 2, MemoryMB: 2048})
			}))
			defer provider.Close()
			c := newAdmissionClient(t, s, provider.URL, 1)
			lease, err := s.AdmissionBegin(ctx, "app:owned", "", "tpl-reviewed", "create", "original-token")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.AdmissionFinish(ctx, lease, "vm-owned", "active"); err != nil {
				t.Fatal(err)
			}
			before, err := s.AdmissionLookup(ctx, "vm-owned")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Connect(ctx, "vm-owned", cube.ConnectRequest{}); !errors.Is(err, cube.ErrRuntimeUnavailable) {
				t.Fatalf("unexpected unavailable connect: %v", err)
			}
			after, err := s.AdmissionLookup(ctx, "vm-owned")
			if err != nil {
				t.Fatal(err)
			}
			if after != before || after.Charged != 1 || after.State != "active" {
				t.Fatalf("lost or changed retained reservation: before=%+v after=%+v", before, after)
			}
			if _, err = s.AdmissionBegin(ctx, "app:other", "", "tpl-reviewed", "create", "other-token"); !errors.Is(err, cube.ErrCapacityUnavailable) {
				t.Fatalf("unavailable task silently freed capacity: %v", err)
			}
			if mutations.Load() != 0 {
				t.Fatal("unavailable connect mutated provider")
			}
		})
	}
}
