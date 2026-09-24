package migration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
)

func TestAdoptRecoveryAuthenticatesWithoutImportReadyOrCutover(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(map[bool]string{true: "exact-owner", false: "wrong-owner"}[matching], func(t *testing.T) {
			engine, fixture, id, _ := fixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			engine.AfterPhase = func(phase string) error {
				if phase == "archived" {
					return errors.New("stop before create")
				}
				return nil
			}
			if e := engine.Run(ctx, id); e == nil {
				t.Fatal("fixture did not stop")
			}
			cipher, e := secrets.Load("", filepath.Join(t.TempDir(), "key"))
			if e != nil {
				t.Fatal(e)
			}
			token := strings.Repeat("a", 64)
			plain, _ := json.Marshal(credentials{SupervisorToken: token})
			enc, nonce, e := cipher.Seal(plain)
			if e != nil {
				t.Fatal(e)
			}
			if e = engine.Store.PrepareMigrationTargetCredential(ctx, id, enc, nonce); e != nil {
				t.Fatal(e)
			}
			statusCalls, deleted := 0, false
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /sandboxes/known":
					owner := id
					if !matching {
						owner = "other-sandbox"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"sandboxID": "known", "templateID": "trusted-template", "trafficAccessToken": "fixture-traffic", "metadata": map[string]string{"sandboxd_id": owner, "sandboxd_app_id": "durable-app", "sandboxd_migration": "offline-v1"}})
				case "POST /sandboxes/known/connect":
					_ = json.NewEncoder(w).Encode(map[string]string{"sandboxID": "known"})
				case "GET /status":
					statusCalls++
					if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("cube-traffic-access-token") != "fixture-traffic" {
						t.Error("adopt replaced original scoped credentials")
						w.WriteHeader(403)
						return
					}
					_, _ = w.Write([]byte(`{"active_task":null,"preview":{"status":"starting"}}`))
				case "DELETE /sandboxes/known":
					deleted = true
					w.WriteHeader(204)
				default:
					t.Errorf("recovery performed forbidden operation %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer host.Close()
			client, e := cube.New(cube.Config{APIURL: host.URL, APIKey: "operator"})
			if e != nil {
				t.Fatal(e)
			}
			backend := fixture.OfflineBackend
			backend.Cube = client
			backend.Secrets = cipher
			backend.ProxyURL = host.URL
			m, e := engine.Store.GetRuntimeMigration(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			e = backend.AdoptTarget(ctx, m, "known", "")
			if !matching {
				if e == nil || statusCalls != 0 {
					t.Fatal("foreign runtime adopted/probed")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			adopted, e := engine.Store.GetRuntimeMigration(ctx, id)
			if e != nil || adopted.Phase != "staged" {
				t.Fatal("adoption advanced beyond staged", e)
			}
			sb, e := engine.Store.Get(ctx, id)
			if e != nil || sb.RuntimeProvider != "docker" {
				t.Fatal("adoption cut over source", e)
			}
			if statusCalls == 0 {
				t.Fatal("original supervisor not authenticated")
			}
			before, e := os.ReadFile(filepath.Join(fixture.source, "data/owner.db"))
			if e != nil {
				t.Fatal(e)
			}
			if e = backend.Abort(ctx, id); e != nil || !deleted {
				t.Fatal("adopted cleanup failed", e)
			}
			after, e := os.ReadFile(filepath.Join(fixture.source, "data/owner.db"))
			if e != nil || string(before) != string(after) {
				t.Fatal("recovery altered owner data")
			}
		})
	}
}
