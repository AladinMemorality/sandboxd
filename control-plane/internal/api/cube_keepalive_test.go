package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeControllerKeepaliveIsBoundedAndDoesNotStartStoppedSandbox(t *testing.T) {
	s, appID := newConfigTestServer(t)
	s.Cube, _ = cube.New(cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"})
	s.CubeAllApps = true
	s.CubeReadiness = func(context.Context) error { return nil }
	s.KeepaliveMax = time.Minute
	sid := newULID()
	ctx := context.Background()
	if err := s.Store.Create(ctx, &store.Sandbox{ID: sid, Status: "stopped", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-keepalive", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: []byte("fixture"), TokenNonce: []byte("fixture")}}); err != nil {
		t.Fatal(err)
	}
	h, err := s.CubeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	request := func(path, body, owner string) *http.Request {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		return r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: owner, Kind: "service"}))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("/sandbox/"+sid+"/keepalive", fmt.Sprintf(`{"until":%d}`, start.Add(24*time.Hour).Unix()), cfgTenant))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	row, err := s.Store.Get(ctx, sid)
	if err != nil || row.Status != "stopped" || !row.KeepaliveUntil.Valid || row.KeepaliveUntil.Int64 < start.Add(59*time.Second).Unix() || row.KeepaliveUntil.Int64 > time.Now().Add(time.Minute).Unix() {
		t.Fatalf("unbounded or mutating keepalive: %+v %v", row, err)
	}
	previous := row.KeepaliveUntil
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("/sandbox/"+sid+"/keepalive", `{"until":1}`, cfgTenant))
	row, _ = s.Store.Get(ctx, sid)
	if w.Code != 400 || row.KeepaliveUntil != previous {
		t.Fatal("expired keepalive accepted")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("/sandbox/missing/keepalive", `{"until":1}`, cfgTenant))
	if w.Code != 404 {
		t.Fatal("unknown sandbox accepted")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("/sandbox/"+sid+"/keepalive", fmt.Sprintf(`{"until":%d}`, time.Now().Add(time.Minute).Unix()), "another-owner"))
	row, _ = s.Store.Get(ctx, sid)
	if w.Code != 404 || row.KeepaliveUntil != previous {
		t.Fatal("another owner's keepalive changed")
	}
}
