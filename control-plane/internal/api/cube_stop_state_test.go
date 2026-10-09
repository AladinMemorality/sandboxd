package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeStopVerifiesStaleLocalStoppedState(t *testing.T) {
	for _, tc := range []struct {
		name, native, guest string
		want, pauses        int
	}{
		{"active VM", "running", `{}`, 200, 1},
		{"active task", "running", `{"active_task":{"id":"working"}}`, 409, 0},
		{"unreachable supervisor", "running", "unavailable", 503, 0},
		{"failed VM", "unknown", `{}`, 503, 0},
		{"unreachable provider", "unavailable", `{}`, 502, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, appID := newConfigTestServer(t)
			var reads, pauses atomic.Int32
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					reads.Add(1)
					if tc.native == "unavailable" {
						http.Error(w, "unavailable", 503)
						return
					}
					json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-stale", TemplateID: "tpl-safe", State: tc.native})
					return
				}
				if r.Method != "POST" || r.URL.Path != "/sandboxes/vm-stale/pause" {
					t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
				}
				pauses.Add(1)
				w.WriteHeader(204)
			}))
			defer management.Close()
			var err error
			s.Cube, err = cube.New(cube.Config{APIURL: management.URL, APIKey: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.guest == "unavailable" {
					http.Error(w, "unavailable", 503)
					return
				}
				w.Write([]byte(tc.guest))
			}))
			defer guest.Close()
			s.CubeProxyURL = guest.URL
			plain, _ := json.Marshal(cubeCredentials{SupervisorToken: strings.Repeat("a", 64), TrafficAccessToken: "synthetic"})
			sealed, nonce, err := s.Secrets.Seal(plain)
			if err != nil {
				t.Fatal(err)
			}
			sb := &store.Sandbox{ID: "stale-stop", Status: "stopped", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-stale", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: sealed, TokenNonce: nonce}}
			if err := s.Store.Create(context.Background(), sb); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/v1/sandboxes/"+sb.ID+"/stop", nil)
			req = req.WithContext(auth.WithActor(req.Context(), auth.Actor{Name: cfgTenant, Kind: "service"}))
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want || int(pauses.Load()) != tc.pauses || reads.Load() == 0 {
				t.Fatalf("status=%d pauses=%d reads=%d body=%s", rec.Code, pauses.Load(), reads.Load(), rec.Body.String())
			}
		})
	}
}
