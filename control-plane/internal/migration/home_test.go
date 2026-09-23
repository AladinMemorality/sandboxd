package migration

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func TestHomeJournalReverseCopyPreservesOwnerWritesAndRejectsOverwrite(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("host ownership restoration requires root fixture")
	}
	engine, backend, id, _ := fixture(t)
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, id)
	target := filepath.Join(root, "guest")
	manifest := runtime.HomeManifest{Version: 1, Entries: []runtime.HomeManifestEntry{{Path: "workspace/app", Disposition: "separate"}, {Path: ".runtimed", Disposition: "separate"}, {Path: "owner-tools", Disposition: "preserve"}, {Path: ".claude", Disposition: "retained", Reason: "provider sessions stay with original Docker identity"}}}
	for _, home := range []string{source, target} {
		for _, path := range []string{"workspace/app", ".runtimed", "owner-tools"} {
			if e := os.MkdirAll(filepath.Join(home, path), 0755); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := os.MkdirAll(filepath.Join(source, ".claude"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, ".claude", "auth.json"), []byte("retained credential fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, "owner-tools", "config"), []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := runtime.CanonicalHomeManifest(manifest)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = engine.Store.DB().ExecContext(ctx, `UPDATE runtime_migration SET home_manifest_json=?,phase='quiesced' WHERE sandbox_id=?`, string(raw), id); e != nil {
		t.Fatal(e)
	}
	backend.WorkspaceRoot = root
	m, e := engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = backend.archiveSourceHome(ctx, m); e != nil {
		t.Fatal(e)
	}
	m, e = engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil || m.HomeSHA256 == "" {
		t.Fatal("home digest not durable", e)
	}
	file, size, e := backend.readHomeArchive(m, "source-home", m.HomeSHA256)
	if e != nil {
		t.Fatal(e)
	}
	if e = runtime.InstallPrivateHome(ctx, target, manifest, file, size); e != nil {
		t.Fatal(e)
	}
	file.Close()
	if _, e = os.Stat(filepath.Join(target, ".claude", "auth.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("retained provider auth was transported")
	}
	if e = os.WriteFile(filepath.Join(target, "owner-tools", "config"), []byte("new Cube writes"), 0600); e != nil {
		t.Fatal(e)
	}
	digest, e := backend.saveHomeArchive(ctx, m, "rollback-home", func(w io.Writer) error { return runtime.ExportPrivateHome(ctx, target, manifest, w) })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = engine.Store.DB().ExecContext(ctx, `UPDATE runtime_migration SET phase='rollback_started' WHERE sandbox_id=?`, id); e != nil {
		t.Fatal(e)
	}
	if e = engine.Store.RecordMigrationHome(ctx, id, "rollback_started", digest, true); e != nil {
		t.Fatal(e)
	}
	m, e = engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = backend.restoreSourceHome(ctx, m); e != nil {
		t.Fatal(e)
	}
	if e = backend.restoreSourceHome(ctx, m); e != nil {
		t.Fatal("recovery is not idempotent", e)
	}
	data, e := os.ReadFile(filepath.Join(source, "owner-tools", "config"))
	if e != nil || string(data) != "new Cube writes" {
		t.Fatal("home new writes missing", e)
	}
	data, e = os.ReadFile(filepath.Join(source, ".claude", "auth.json"))
	if e != nil || string(data) != "retained credential fixture" {
		t.Fatal("original provider state changed", e)
	}
	if _, e = backend.saveHomeArchive(ctx, m, "source-home", func(w io.Writer) error { return runtime.ExportPrivateHome(ctx, source, manifest, w) }); e == nil {
		t.Fatal("original recovery artifact overwritten")
	}
}
