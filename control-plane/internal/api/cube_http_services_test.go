package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
)

func TestCubeAppHTTPServicesRequireExactPersistedAppAndChannelIdentity(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	ctx := context.Background()
	row, err := s.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	service := egress.HTTPService{Origin: "http://10.40.14.68:8080", Routes: map[string][]string{"GET": {"/health"}}}
	s.cubeEgress = &cubeEgressManager{config: CubeEgressConfig{AppHTTPServices: map[string][]egress.HTTPService{
		"another-app": {service},
	}}}
	if got := s.cubeAppHTTPServices(ctx, id, cubeEgressGeneration(binding)); len(got) != 0 {
		t.Fatal("another app or remix inherited private service")
	}
	s.cubeEgress.config.AppHTTPServices[row.AppID.String] = []egress.HTTPService{service}
	got := s.cubeAppHTTPServices(ctx, id, cubeEgressGeneration(binding))
	if len(got) != 1 || got[service.Address()] == nil {
		t.Fatal("operator app service missing")
	}
	if err = s.Store.MarkRunning(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	}
	// Calling the handler without the authenticated reverse channel cannot
	// impersonate its persisted sandbox, even with a valid application key.
	r := httptest.NewRequest("GET", "/health", nil)
	r.Header.Set("Authorization", "Bearer app-scoped-key")
	w := httptest.NewRecorder()
	got[service.Address()].ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("missing channel identity accepted: %d", w.Code)
	}
	if got := s.cubeAppHTTPServices(ctx, "missing-runtime", "stale"); len(got) != 0 {
		t.Fatal("unknown sandbox inherited app service")
	}
}
