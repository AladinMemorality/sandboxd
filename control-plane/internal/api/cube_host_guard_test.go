package api

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubePublishAndPurgeCannotTouchHost(t *testing.T) {
	s, appID := newConfigTestServer(t)
	s.LibraryRoot = t.TempDir()
	sb := &store.Sandbox{ID: "cube-no-host", Status: "stopped", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, ExternalUserID: sql.NullString{String: "cube-user", Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-no-host", TemplateID: "tpl", Domain: "cube.test", TokenCiphertext: []byte("sealed"), TokenNonce: []byte("nonce")}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tenant string
		want   int
	}{{cfgTenant, 503}, {"another-tenant", 404}} {
		r := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(`{"source_sandbox_id":"cube-no-host","name":"published"}`))
		r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: tc.tenant, Kind: "service"}))
		w := httptest.NewRecorder()
		s.v1CreateSnapshot(w, r)
		if w.Code != tc.want {
			t.Fatalf("snapshot %s: %d %s", tc.tenant, w.Code, w.Body)
		}
	}
	if _, _, err := s.purgeOne(context.Background(), sb.ID); err == nil {
		t.Fatal("Cube entered host purge")
	}
	w := httptest.NewRecorder()
	s.purgeScope(w, httptest.NewRequest("DELETE", "/external/user/cube-user", nil), "user", "cube-user")
	if w.Code != 501 {
		t.Fatalf("bulk purge: %d %s", w.Code, w.Body)
	}
	if _, err := s.Store.Get(context.Background(), sb.ID); err != nil {
		t.Fatal("Cube row removed", err)
	}
}

func TestCubeAlternateRoutesPreserveOwnerAndProvider(t *testing.T) {
	s, appID := newConfigTestServer(t)
	sb := &store.Sandbox{ID: "cube-alternate", Status: "running", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, ExternalProjectID: sql.NullString{String: "cube-project", Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-alternate", TemplateID: "tpl", Domain: "cube.test", TokenCiphertext: []byte("sealed"), TokenNonce: []byte("nonce")}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body, tenant string
		want                       int
	}{
		{"POST", "/v1/sandboxes", `{"project":{"id":"cube-project","user_id":"attacker"}}`, "attacker", 404},
		{"POST", "/sandbox", `{"app_id":"` + appID + `"}`, "attacker", 404},
		{"POST", "/sandbox", `{"app_id":"` + appID + `"}`, cfgTenant, 501},
		{"GET", "/sandboxes", "", "attacker", 200},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: tc.tenant, Kind: "service"}))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), sb.ID) {
			t.Errorf("private identity leaked: %s", w.Body)
		}
	}
}
