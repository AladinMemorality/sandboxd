package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
)

func TestProductionPreviewNeverWakesDevelopmentAndDoesNotFallBackOnFailure(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("production contacted development guest") })
	s.CubeAllApps = true
	s.CubeReadiness = func(context.Context) error { return nil }
	sb, _ := s.Store.Get(context.Background(), cubePreviewTestID)
	if _, err := s.Store.DB().Exec("UPDATE sandbox SET visibility='public',status='stopped' WHERE id=?", sb.ID); err != nil {
		t.Fatal(err)
	}
	s.ProductionToken = strings.Repeat("a", 32)
	failed := false
	production := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Baarcha-Hosting-Token") != s.ProductionToken {
			t.Error("missing hosting credential")
		}
		if failed {
			w.WriteHeader(503)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/projects/") {
			w.Write([]byte(`{"current":"release"}`))
			return
		}
		if r.URL.Path != "/serve/"+sb.AppID.String+"/api/test" {
			t.Errorf("unexpected production path %s", r.URL.Path)
		}
		w.Write([]byte("production backend"))
	}))
	defer production.Close()
	s.ProductionURL = production.URL
	h, err := s.CubeHandler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{cfgTenant, "other"} {
		r := httptest.NewRequest("GET", "/v1/apps/"+sb.AppID.String+"/production", nil)
		r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: tenant, Kind: "service"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := 200
		if tenant == "other" {
			expected = 404
		}
		if w.Code != expected {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	}
	for _, unavailable := range []bool{false, true} {
		failed = unavailable
		r := httptest.NewRequest("GET", strings.Replace(s.previewURL(sb.ID, webPortOf(sb)), "://s-", "://p-", 1)+"/api/test", nil)
		w := httptest.NewRecorder()
		if !s.TryServeCubePreview(w, r) {
			t.Fatal("production host not handled")
		}
		expected := 200
		if failed {
			expected = 503
		}
		if w.Code != expected {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if !failed && w.Body.String() != "production backend" {
			t.Fatal(w.Body.String())
		}
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("production woke development")
	}
}
