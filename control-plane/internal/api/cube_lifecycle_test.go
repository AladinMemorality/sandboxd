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

func TestCubeCreatePreservesIdentityAndPrivateCredentials(t *testing.T) {
	for _, global := range []bool{false, true} {
		name := "allowlist"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) { testCubeCreatePreservesIdentityAndPrivateCredentials(t, global) })
	}
}

func testCubeCreatePreservesIdentityAndPrivateCredentials(t *testing.T, global bool) {
	s, appID := newConfigTestServer(t)
	var sent cube.CreateRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/sandboxes" {
			t.Errorf("unexpected Cube call %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sandboxID":"vm-separate-id","templateID":"tpl-safe","trafficAccessToken":"traffic-secret","domain":"attacker.invalid"}`))
	}))
	defer upstream.Close()
	var err error
	s.Cube, err = cube.New(cube.Config{APIURL: upstream.URL, APIKey: "management-secret"})
	if err != nil {
		t.Fatal(err)
	}
	s.CubeTemplates = map[string]string{"react-vite": "tpl-safe"}
	s.CubeAllApps = global
	if !global {
		s.CubeApps = map[string]bool{appID: true}
	}
	s.CubeProxyURL = upstream.URL
	s.CubeDomain = "trusted.cube.test"
	req := httptest.NewRequest("POST", "/v1/apps/"+appID+"/sandbox", strings.NewReader(`{"runtime_preset":"react-vite"}`))
	req = req.WithContext(auth.WithActor(req.Context(), auth.Actor{Name: cfgTenant, Kind: "service"}))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var response sandboxResp
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID == "" || response.ID == "vm-separate-id" {
		t.Fatalf("lost stable sandbox ID: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "RUNTIMED_HTTP_TOKEN") {
		t.Fatal("credential leaked")
	}
	if sent.TemplateID != "tpl-safe" || sent.Network == nil || sent.Network.AllowPublicTraffic || sent.AllowInternetAccess || sent.Lifecycle.OnTimeout != "pause" {
		t.Fatalf("unsafe create policy: %+v", sent)
	}
	b, err := s.Store.GetRuntimeBinding(context.Background(), response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Domain != "trusted.cube.test" {
		t.Fatal("trusted routing replaced by untrusted response domain")
	}
	plaintext, err := s.Secrets.Open(b.TokenCiphertext, b.TokenNonce)
	if err != nil {
		t.Fatal(err)
	}
	var credentials cubeCredentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		t.Fatal(err)
	}
	if credentials.SupervisorToken != sent.EnvVars["RUNTIMED_HTTP_TOKEN"] || credentials.TrafficAccessToken != "traffic-secret" {
		t.Fatal("runtime credentials not persisted")
	}
	if strings.Contains(string(b.TokenCiphertext), "traffic-secret") {
		t.Fatal("plaintext secret in binding")
	}
}

func TestCubeRoutesFailClosedBeforeDockerAndTenantChecks(t *testing.T) {
	s, appID := newConfigTestServer(t)
	sb := &store.Sandbox{ID: "cube-route-test", Status: "running", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-route-test", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: []byte("sealed"), TokenNonce: []byte("nonce")}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, tenant string
		want                 int
	}{
		{"POST", "/sandbox/" + sb.ID + "/exec", cfgTenant, 501},
		{"POST", "/v1/sandboxes/" + sb.ID + "/recreate", "other-tenant", 404},
		{"GET", "/v1/apps/" + appID + "/git/status", cfgTenant, 501},
		{"GET", "/v1/apps/" + appID + "/runtime-inspect", cfgTenant, 501},
		{"GET", "/v1/sandboxes/" + sb.ID, "other-tenant", 404},
		{"DELETE", "/v1/sandboxes/" + sb.ID, "other-tenant", 404},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: tc.tenant, Kind: "service"}))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestCubePauseConnectDeleteUseSeparateLifecycleOperations(t *testing.T) {
	s, appID := newConfigTestServer(t)
	var actions []string
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions = append(actions, r.Method+" "+r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/connect") {
			_, _ = w.Write([]byte(`{"sandboxID":"vm-lifecycle","templateID":"tpl-safe"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer management.Close()
	var err error
	s.Cube, err = cube.New(cube.Config{APIURL: management.URL, APIKey: "api-secret"})
	if err != nil {
		t.Fatal(err)
	}
	supervisorToken := strings.Repeat("a", 64)
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "3031-vm-lifecycle.cube.test" || r.Header.Get("Authorization") != "Bearer "+supervisorToken || r.Header.Get("cube-traffic-access-token") != "traffic-secret" {
			t.Error("missing scoped supervisor routing/auth")
		}
		if r.Header.Get("X-API-Key") != "" {
			t.Error("management key sent to guest")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer guest.Close()
	s.CubeProxyURL = guest.URL
	credentials, _ := json.Marshal(cubeCredentials{SupervisorToken: supervisorToken, TrafficAccessToken: "traffic-secret"})
	sealed, nonce, err := s.Secrets.Seal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	sb := &store.Sandbox{ID: "cube-lifecycle-test", Status: "running", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-lifecycle", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: sealed, TokenNonce: nonce}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, suffix string
		want           int
	}{{"POST", "/stop", 200}, {"POST", "/start", 200}, {"DELETE", "", 204}} {
		req := httptest.NewRequest(tc.method, "/v1/sandboxes/"+sb.ID+tc.suffix, nil)
		req = req.WithContext(auth.WithActor(req.Context(), auth.Actor{Name: cfgTenant, Kind: "service"}))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.suffix, rec.Code, rec.Body.String())
		}
	}
	want := []string{"POST /sandboxes/vm-lifecycle/pause", "POST /sandboxes/vm-lifecycle/connect", "DELETE /sandboxes/vm-lifecycle"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("unsafe lifecycle mapping: %v", actions)
	}
	if _, err := s.Store.GetRuntimeBinding(context.Background(), sb.ID); err != store.ErrNotFound {
		t.Fatalf("binding survived delete: %v", err)
	}
}

func TestCubeCreateDoesNotAdvertiseUnauthenticatedSupervisor(t *testing.T) {
	s, appID := newConfigTestServer(t)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	var rejected atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/sandboxes" {
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"sandboxID":"vm-bad-supervisor","templateID":"tpl-safe","trafficAccessToken":"traffic-secret"}`))
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		if r.URL.Path == "/status" {
			rejected.Store(true)
			cancelRequest()
		}
	}))
	defer upstream.Close()
	var err error
	s.Cube, err = cube.New(cube.Config{APIURL: upstream.URL, APIKey: "api-secret"})
	if err != nil {
		t.Fatal(err)
	}
	s.CubeApps = map[string]bool{appID: true}
	s.CubeTemplates = map[string]string{"react-vite": "tpl-safe"}
	s.CubeProxyURL = upstream.URL
	s.CubeDomain = "cube.test"
	r := httptest.NewRequest("POST", "/v1/apps/"+appID+"/sandbox", strings.NewReader(`{"runtime_preset":"react-vite"}`))
	r = r.WithContext(auth.WithActor(requestCtx, auth.Actor{Name: cfgTenant, Kind: "service"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if !rejected.Load() {
		t.Fatal("request did not reach rejecting supervisor")
	}
	if w.Code != 502 {
		t.Fatalf("advertised unauthenticated supervisor: %d %s", w.Code, w.Body.String())
	}
	sb, err := s.Store.CurrentSandboxForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Status != "error" {
		t.Fatalf("failed supervisor is %s, want error", sb.Status)
	}
	if _, err := s.Store.GetRuntimeBinding(context.Background(), sb.ID); err != nil {
		t.Fatalf("lost recovery binding: %v", err)
	}
}

func TestCubeDeleteRecoversMissingRemoteAndPurgesOwner(t *testing.T) {
	s, appID := newConfigTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/sandboxes/vm-already-deleted" {
			t.Errorf("unexpected remote call %s %s", r.Method, r.URL.Path)
		}
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer upstream.Close()
	var err error
	s.Cube, err = cube.New(cube.Config{APIURL: upstream.URL, APIKey: "api-secret"})
	if err != nil {
		t.Fatal(err)
	}
	sb := &store.Sandbox{ID: "cube-delete-recovery", Status: "running", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, ExternalUserID: sql.NullString{String: "original-owner", Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-already-deleted", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: []byte("sealed"), TokenNonce: []byte("nonce")}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.GetWorkspaceOwner(context.Background(), sb.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("DELETE", "/v1/sandboxes/"+sb.ID, nil)
	r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: cfgTenant, Kind: "service"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("delete404 recovery: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.Store.Get(context.Background(), sb.ID); err != store.ErrNotFound {
		t.Fatalf("sandbox remains: %v", err)
	}
	if _, err := s.Store.GetRuntimeBinding(context.Background(), sb.ID); err != store.ErrNotFound {
		t.Fatalf("binding remains: %v", err)
	}
	if _, err := s.Store.GetWorkspaceOwner(context.Background(), sb.ID); err != store.ErrNotFound {
		t.Fatalf("workspace owner remains: %v", err)
	}
	if _, err := s.Store.GetAppForOwner(context.Background(), appID, cfgTenant); err != nil {
		t.Fatalf("deleted durable app: %v", err)
	}
}
