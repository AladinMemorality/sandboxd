package api

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeAppChoiceSurvivesSandboxDeletionAndDisabledPilot(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	sb, err := s.Store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if chosen, err := s.Store.AppUsesCube(context.Background(), sb.AppID.String); err != nil || !chosen {
		t.Fatal("Cube provider not durably selected")
	}
	if w := cubeRequest(s, "DELETE", "/v1/sandboxes/"+id, "", cfgTenant); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	s.Cube = nil
	s.CubeApps = nil
	w := cubeRequest(s, "POST", "/v1/apps/"+sb.AppID.String+"/sandbox", `{"runtime_preset":"react-vite"}`, cfgTenant)
	if w.Code != 503 {
		t.Fatalf("persisted Cube app fell through to Docker: %d %s", w.Code, w.Body.String())
	}
	w = cubeRequest(s, "POST", "/sandbox", `{"app_id":"`+sb.AppID.String+`"}`, cfgTenant)
	if w.Code != 501 {
		t.Fatalf("legacy create downgraded Cube app: %d %s", w.Code, w.Body.String())
	}
	if err := s.Store.Create(context.Background(), &store.Sandbox{ID: "new-docker-id", Status: "running", AppID: sql.NullString{String: sb.AppID.String, Valid: true}}); err != store.ErrConflict {
		t.Fatalf("store accepted provider downgrade: %v", err)
	}
}

func TestCubeAppDeletePurgesVMAndPersistentProvider(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	sb, err := s.Store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	w := cubeRequest(s, "DELETE", "/v1/apps/"+sb.AppID.String, "", "other-owner")
	if w.Code != 404 {
		t.Fatalf("cross-owner app deletion: %d", w.Code)
	}
	w = cubeRequest(s, "DELETE", "/v1/apps/"+sb.AppID.String, "", cfgTenant)
	if w.Code != 204 {
		t.Fatalf("delete app: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.Store.Get(context.Background(), id); err != store.ErrNotFound {
		t.Fatalf("sandbox remains: %v", err)
	}
	if _, err := s.Store.GetApp(context.Background(), sb.AppID.String); err != store.ErrNotFound {
		t.Fatalf("app remains: %v", err)
	}
	if selected, err := s.Store.AppUsesCube(context.Background(), sb.AppID.String); err != nil || selected {
		t.Fatalf("provider survives deleted app: %v %v", selected, err)
	}
}

func TestLegacySnapshotCreateCannotSendCubeSourceArchiveToDocker(t *testing.T) {
	s, _ := newConfigTestServer(t)
	snap := &store.Snapshot{ID: newULID(), Name: "source", OwnerToken: cfgTenant, Status: "ready", Format: cubeSourceFormat, ImagePath: "/must/not/be/read.cube.zip"}
	if err := s.Store.CreateSnapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	w := cubeRequest(s, "POST", "/v1/sandboxes", `{"project":{"id":"new-project","user_id":"user"},"from_snapshot":"`+snap.ID+`"}`, cfgTenant)
	if w.Code != 501 {
		t.Fatalf("Cube source fell through to Docker provisioning: %d %s", w.Code, w.Body.String())
	}
}
