package migration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestFleetUsesHistoricalDockerIdentityContract(t *testing.T) {
	full := strings.Repeat("a", 64)
	for _, recorded := range []string{full, full[:12], full[:11], strings.Repeat("b", 12), "named-container"} {
		t.Run(recorded, func(t *testing.T) {
			engine, _, id, _ := fixture(t)
			ctx := context.Background()
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, id, "workspace", "app"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := engine.Store.MarkRunning(ctx, id, recorded, "group"); err != nil {
				t.Fatal(err)
			}
			options := FleetOptions{Templates: map[string]string{"react-vite": "reviewed"}, AppPresets: map[string]string{"durable-app": "react-vite"}, LibraryRoot: filepath.Join(root, "library"), TemplateResources: map[string]ResourceLimits{"reviewed": {2000, 2 << 30}}, InspectSource: func(context.Context, string) (*docker.ContainerJSON, error) {
				return resourceFixtureContainer(full), nil
			}}
			report, err := FleetPreflight(ctx, engine.Store.DB(), root, options)
			if err != nil {
				t.Fatal(err)
			}
			wantEligible := recorded == full || recorded == full[:12]
			if (report.BlockedProjects == 0) != wantEligible {
				t.Fatalf("Docker identity eligibility differs: %+v", report.Projects)
			}
		})
	}
}

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
	options := FleetOptions{Templates: map[string]string{"react-vite": "reviewed"}, LibraryRoot: library, TemplateResources: map[string]ResourceLimits{"reviewed": {2000, 2 << 30}}, InspectSource: func(context.Context, string) (*docker.ContainerJSON, error) {
		return resourceFixtureContainer("fixture"), nil
	}}
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
	options.TemplateResources["reviewed"] = ResourceLimits{1000, 1 << 30}
	undersized, e := FleetPreflight(ctx, engine.Store.DB(), root, options)
	if e != nil || undersized.BlockedProjects != 1 {
		t.Fatalf("fleet accepted half-sized target: %+v %v", undersized, e)
	}
	options.TemplateResources["reviewed"] = ResourceLimits{2000, 2 << 30}
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
