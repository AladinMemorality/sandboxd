package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func TestCubeCreateReportsCapacityAndPendingWithoutProviderMutation(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "capacity"
		if pending {
			name = "pending"
		}
		t.Run(name, func(t *testing.T) {
			s, appID := newConfigTestServer(t)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(500)
			}))
			defer provider.Close()
			var err error
			s.Cube, err = cube.New(cube.Config{APIURL: provider.URL, APIKey: "synthetic-management"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			err = s.Cube.ConfigureAdmission(ctx, s.Store, cube.AdmissionConfig{
				MaxActive: 1, CPUCount: 2, MemoryMB: 2048,
				Templates: map[string]cube.AdmissionResources{"tpl-safe": {CPUCount: 2, MemoryMB: 2048}},
			})
			if err != nil {
				t.Fatal(err)
			}
			key := "app:other-fixture"
			if pending {
				key = "app:" + appID
			}
			if _, err = s.Store.AdmissionBegin(ctx, key, "", "tpl-safe", "create", "fixture-operation"); err != nil {
				t.Fatal(err)
			}
			s.CubeTemplates = map[string]string{"react-vite": "tpl-safe"}
			s.CubeAllApps = true
			s.CubeProxyURL = provider.URL
			s.CubeDomain = "cube.test"
			r := httptest.NewRequest("POST", "/v1/apps/"+appID+"/sandbox", strings.NewReader(`{"runtime_preset":"react-vite"}`))
			r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: cfgTenant, Kind: "service"}))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			wantStatus, wantCode, wantRetry := 503, "runtime_capacity", "5"
			if pending {
				wantStatus, wantCode, wantRetry = 409, "runtime_reconciliation_required", ""
			}
			if w.Code != wantStatus || !strings.Contains(w.Body.String(), wantCode) || w.Header().Get("Retry-After") != wantRetry {
				t.Fatalf("unexpected admission response: %d %s retry=%q", w.Code, w.Body.String(), w.Header().Get("Retry-After"))
			}
			if calls.Load() != 0 {
				t.Fatal("rejected request contacted provider")
			}
			if strings.Contains(w.Body.String(), "synthetic-management") || strings.Contains(w.Body.String(), "fixture-operation") {
				t.Fatal("internal admission values leaked")
			}
		})
	}
}
