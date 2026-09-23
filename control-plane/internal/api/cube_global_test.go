package api

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestGlobalCubeRejectsLegacyCreationAndCrossOwnerBeforeProvisioning(t *testing.T) {
	s, appID := newConfigTestServer(t)
	s.CubeAllApps = true
	for _, tc := range []struct {
		path, body, owner string
		code              int
	}{
		{"/sandbox", `{}`, cfgTenant, 501},
		{"/sandbox", `{"app_id":"` + appID + `"}`, cfgTenant, 501},
		{"/sandbox", `{"app_id":"` + appID + `"}`, "other-owner", 404},
		{"/v1/apps/" + appID + "/sandbox", `{"runtime_preset":"react-vite"}`, cfgTenant, 503},
		{"/v1/apps/" + appID + "/sandbox", `{"runtime_preset":"react-vite"}`, "other-owner", 404},
	} {
		w := cubeRequest(s, "POST", tc.path, tc.body, tc.owner)
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestGlobalLegacyRestoreNeverPurgesDockerBeforeMigration(t *testing.T) {
	s, appID := newConfigTestServer(t)
	s.CubeAllApps = true
	s.Cube, _ = cube.New(cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "test"})
	s.CubeTemplates = map[string]string{"react-vite": "tpl-reviewed"}
	s.LibraryRoot = t.TempDir()
	sourceApp := &store.App{ID: newULID(), OwnerToken: cfgTenant, Name: "Source", RuntimePreset: sql.NullString{String: "react-vite", Valid: true}}
	if err := s.Store.CreateApp(context.Background(), sourceApp); err != nil {
		t.Fatal(err)
	}
	id := newULID()
	snap := &store.Snapshot{ID: id, Name: "legacy", OwnerToken: cfgTenant, Status: "ready", Format: "raw", ImagePath: filepath.Join(s.LibraryRoot, id), SourceAppID: sql.NullString{String: sourceApp.ID, Valid: true}}
	appRoot := filepath.Join(snap.ImagePath, "workspace", "app")
	if err := os.MkdirAll(appRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "package.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.CreateSnapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	sb := &store.Sandbox{ID: newULID(), Status: "stopped", RuntimeProvider: "docker", AppID: sql.NullString{String: appID, Valid: true}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	w := cubeRequest(s, "POST", "/v1/apps/"+appID+"/restore", `{"snapshot_id":"`+id+`"}`, cfgTenant)
	if w.Code != 409 {
		t.Fatalf("restore should require explicit migration: %d %s", w.Code, w.Body.String())
	}
	current, err := s.Store.CurrentSandboxForApp(context.Background(), appID)
	if err != nil || current.ID != sb.ID || current.RuntimeProvider != "docker" {
		t.Fatal("restore removed unmigrated Docker source")
	}
	snap.ImagePath = filepath.Join(s.LibraryRoot, "outside")
	if _, _, err := s.readSourceForCube(context.Background(), snap); err == nil {
		t.Fatal("unexpected artifact path accepted")
	}
	snap.ImagePath = filepath.Join(s.LibraryRoot, id)
	snap.OwnerToken = "other-owner"
	if _, _, err := s.readSourceForCube(context.Background(), snap); err == nil {
		t.Fatal("cross-owner source accepted")
	}
}

func TestGlobalCubePreservesExistingDockerAndRejectsReservedNewAppConfig(t *testing.T) {
	s, appID := newConfigTestServer(t)
	s.CubeAllApps = true
	base := "/v1/apps/" + appID
	w := cubeRequest(s, "POST", base+"/config", `{"key":"RUNTIMED_HTTP_TOKEN","value":"override","access_policy":"runtime_access"}`, cfgTenant)
	if w.Code != 400 {
		t.Fatalf("reserved key accepted before creation: %d %s", w.Code, w.Body.String())
	}
	sb := &store.Sandbox{ID: newULID(), Status: "stopped", RuntimeProvider: "docker", AppID: sql.NullString{String: appID, Valid: true}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	w = cubeRequest(s, "POST", base+"/sandbox", `{"runtime_preset":"react-vite"}`, cfgTenant)
	if w.Code != 409 {
		t.Fatalf("existing Docker replaced: %d %s", w.Code, w.Body.String())
	}
	current, err := s.Store.CurrentSandboxForApp(context.Background(), appID)
	if err != nil || current.ID != sb.ID || current.RuntimeProvider != "docker" {
		t.Fatal("global mode changed existing provider")
	}
}
