package api

import (
	"context"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectDeploymentAPIHonorsOwnerAndDoesNotInventSourceRevision(t *testing.T) {
	s, h := controllerFixture(t)
	id := newULID()
	if err := s.Store.CreateApp(context.Background(), &store.App{ID: id, OwnerToken: cfgTenant, Name: "fixture"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner  string
		status int
	}{{cfgTenant, 200}, {"other-owner", 404}} {
		req := httptest.NewRequest("GET", "/v1/apps/"+id+"/deployment", nil)
		req = req.WithContext(auth.WithActor(req.Context(), auth.Actor{Name: tc.owner, Kind: "service"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("owner=%s: %d %s", tc.owner, w.Code, w.Body)
		}
		if w.Code == 200 && !strings.Contains(w.Body.String(), `"has_source_revision":false`) {
			t.Fatal("invented source revision", w.Body)
		}
	}
}
