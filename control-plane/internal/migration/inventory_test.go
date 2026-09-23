package migration

import (
	"context"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"os"
	"path/filepath"
	"testing"
)

func TestInventoryReportsRealHomeAndTransportLimits(t *testing.T) {
	engine, _, id, _ := fixture(t)
	root := t.TempDir()
	home := filepath.Join(root, id)
	app := filepath.Join(home, "workspace", "app")
	if err := os.MkdirAll(app, 0755); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.MarkRunning(context.Background(), id, "container", "cgroup"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "index.js"), []byte("code"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "custom-owner-data"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := Inventory(context.Background(), engine.Store.DB(), root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Eligible || len(rows[0].UnhandledHomePaths) != 1 || rows[0].UnhandledHomePaths[0] != "custom-owner-data" || rows[0].CompressedArchiveLimit != runtime.MaxPrivateWorkspaceStreamBytes {
		t.Fatalf("incomplete inventory: %+v", rows)
	}
	if err = os.Remove(filepath.Join(home, "custom-owner-data")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/usr/bin/python", filepath.Join(app, "python")); err != nil {
		t.Fatal(err)
	}
	rows, err = Inventory(context.Background(), engine.Store.DB(), root, id)
	if err != nil || rows[0].Eligible {
		t.Fatal("absolute runtime symlink silently accepted", err)
	}
	if err = os.Remove(filepath.Join(app, "python")); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(app, ".venv", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/usr/bin/python3", filepath.Join(app, ".venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}
	rows, err = Inventory(context.Background(), engine.Store.DB(), root, id)
	if err != nil || !rows[0].Eligible {
		t.Fatalf("approved private interpreter leaf rejected: %+v %v", rows, err)
	}
}
