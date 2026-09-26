package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func controllerFixture(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, _ := newConfigTestServer(t)
	s.Cube, _ = cube.New(cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"})
	s.CubeAllApps = true
	s.CubeReadiness = func(context.Context) error { return nil }
	h, err := s.CubeHandler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, h
}

func TestCubeControllerRefusesDockerDependenciesAndFleet(t *testing.T) {
	s, _ := controllerFixture(t)
	s.Docker = docker.NewClient()
	if _, err := s.CubeHandler(context.Background()); err == nil {
		t.Fatal("Docker client accepted")
	}
	s.Docker = nil
	if err := s.Store.Create(context.Background(), &store.Sandbox{ID: newULID(), Status: "stopped"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CubeHandler(context.Background()); err == nil {
		t.Fatal("unmigrated fleet accepted")
	}
}

func TestCubeControllerNoLegacyExecutionAndNoDockerReadiness(t *testing.T) {
	s, h := controllerFixture(t)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/healthz", 200}, {"GET", "/readyz", 200},
		{"POST", "/sandbox", 404}, {"POST", "/v1/sandboxes", 404},
		{"POST", "/sandbox/legacy/exec", 404}, {"POST", "/v1/upgrade", 404},
		{"GET", "/v1/apps", 200}, {"GET", "/sandboxes", 200}, {"GET", "/v1/agents", 200},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: cfgTenant, Kind: "service"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	s.CubeReadiness = func(context.Context) error { return errors.New("private worker details") }
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := s.runtimeClientFor("missing").Status(context.Background()); err == nil {
		t.Fatal("missing binding fell through to a host socket")
	}
}

func TestCubeControllerRejectsProviderDriftAndUnknownPreview(t *testing.T) {
	s, h := controllerFixture(t)
	s.PreviewDomain = "example.test"
	preview := s.CubePreviewHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preview reached API") }))
	r := httptest.NewRequest("GET", "http://s-01ABCDEFGHJKMNPQRSTVWXYZ012-3000.preview.example.test/v1/apps", nil)
	w := httptest.NewRecorder()
	preview.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if err := s.Store.Create(context.Background(), &store.Sandbox{ID: newULID(), Status: "stopped"}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(`{}`)))
	if w.Code != 503 {
		t.Fatal("provider drift reached a handler", w.Code, w.Body)
	}
}
