package migration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestFleetAccountsForProjectsSnapshotsAndMembershipDrift(t *testing.T) {
	engine, _, id, _ := fixture(t)
	ctx := context.Background()
	root := t.TempDir()
	library := filepath.Join(root, "library")
	home := filepath.Join(root, id, "workspace", "app")
	if e := os.MkdirAll(home, 0755); e != nil {
		t.Fatal(e)
	}
	if e := engine.Store.MarkRunning(ctx, id, "fixture", "group"); e != nil {
		t.Fatal(e)
	}
	snapshotID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	snapshotPath := filepath.Join(library, snapshotID)
	if e := os.MkdirAll(filepath.Join(snapshotPath, "workspace", "app"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := engine.Store.CreateSnapshot(ctx, &store.Snapshot{ID: snapshotID, Name: "published", OwnerToken: "owner", SourceAppID: sql.NullString{String: "durable-app", Valid: true}, BaseImage: "legacy", Format: "raw", Status: "ready", ImagePath: snapshotPath, Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	options := FleetOptions{Templates: map[string]string{"react-vite": "reviewed"}, LibraryRoot: library}
	report, e := FleetPreflight(ctx, engine.Store.DB(), root, options)
	if e != nil {
		t.Fatal(e)
	}
	if report.SandboxCount != 1 || len(report.Projects) != 1 || report.BlockedProjects != 1 || report.BlockedSnapshots != 1 || report.RawSnapshotCount != 1 || report.MissingSnapshotPresetCount != 1 {
		t.Fatalf("incomplete inventory: %+v", report)
	}
	options.AppPresets = map[string]string{"durable-app": "react-vite"}
	reviewed, e := FleetPreflight(ctx, engine.Store.DB(), root, options)
	if e != nil {
		t.Fatal(e)
	}
	if reviewed.BlockedProjects != 0 || reviewed.BlockedSnapshots != 0 || reviewed.Snapshots[0].State != "conversion_required" {
		t.Fatalf("reviewed plan: %+v", reviewed)
	}
	if e = VerifyFleetIdentity(ctx, engine.Store.DB(), report.IdentitySHA256); e != nil {
		t.Fatal(e)
	}
	if e = engine.Store.CreateApp(ctx, &store.App{ID: "new-project", Name: "new", OwnerToken: "owner"}); e != nil {
		t.Fatal(e)
	}
	if e = VerifyFleetIdentity(ctx, engine.Store.DB(), report.IdentitySHA256); e == nil {
		t.Fatal("new project escaped fleet fence")
	}
	if _, e = engine.Store.DB().ExecContext(ctx, `DELETE FROM app WHERE id='new-project'`); e != nil {
		t.Fatal(e)
	}
	if _, e = engine.Store.DB().ExecContext(ctx, `UPDATE app SET owner_token='different' WHERE id='durable-app'`); e != nil {
		t.Fatal(e)
	}
	if e = VerifyFleetIdentity(ctx, engine.Store.DB(), report.IdentitySHA256); e == nil {
		t.Fatal("ownership change escaped fleet fence")
	}
}

func TestFleetSnapshotImmutablePresetAndUnsafeRawPath(t *testing.T) {
	engine, _, _, _ := fixture(t)
	ctx := context.Background()
	root := t.TempDir()
	library := filepath.Join(root, "library")
	for i, format := range []string{"raw", "cube-source-v1"} {
		id := []string{"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"}[i]
		snap := &store.Snapshot{ID: id, Name: format, OwnerToken: "owner", BaseImage: "cube-preset:node-express", Format: format, Status: "ready", ImagePath: filepath.Join(root, "outside"), Visibility: "private"}
		if format == "cube-source-v1" {
			snap.ImagePath = filepath.Join(library, id+".cube.zip")
			if e := os.MkdirAll(library, 0755); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(snap.ImagePath, []byte("metadata-only fixture"), 0440); e != nil {
				t.Fatal(e)
			}
		}
		if format == "raw" {
			snap.SourceAppID = sql.NullString{String: "durable-app", Valid: true}
		}
		if e := engine.Store.CreateSnapshot(ctx, snap); e != nil {
			t.Fatal(e)
		}
	}
	options := FleetOptions{Templates: map[string]string{"react-vite": "react", "node-express": "node"}, AppPresets: map[string]string{"durable-app": "react-vite"}, LibraryRoot: library}
	report, e := FleetPreflight(ctx, engine.Store.DB(), root, options)
	if e != nil {
		t.Fatal(e)
	}
	if report.Snapshots[0].State != "blocked" || report.Snapshots[1].Preset != "node-express" || report.Snapshots[1].State != "preflight_passed" {
		t.Fatalf("unsafe or mutable snapshot mapping: %+v", report.Snapshots)
	}
}
