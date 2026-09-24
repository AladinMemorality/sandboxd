package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Only the hypervisor is a fixture: archive verification, filesystem restore,
// ownership normalization, SQLite journal and atomic provider commit are real.
type backupRestoreBackend struct{ *fixtureBackend }

func (b *backupRestoreBackend) RestoreSource(ctx context.Context, m *store.RuntimeMigration) error {
	return b.OfflineBackend.RestoreSource(ctx, m)
}

// A migration ZIP is deliberately NOT a full machine backup: provider state,
// runtime identity and encryption keys stay outside transport. This test restores
// all independent operator backup scopes after removing their original copies.
func TestIndependentBackupRestoreResumesRollbackWithoutOriginalFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("host recovery ownership requires root fixture")
	}
	ctx := context.Background()
	engine, f, id, dbPath := fixture(t)
	root := filepath.Dir(dbPath)
	home := filepath.Join(root, "workspaces", id)
	app := filepath.Join(home, "workspace", "app")
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	put := func(path, data string) {
		t.Helper()
		must(os.MkdirAll(filepath.Dir(path), 0755))
		must(os.WriteFile(path, []byte(data), 0600))
	}
	must(os.MkdirAll(filepath.Dir(app), 0755))
	must(os.Rename(f.source, app))
	f.source = app
	f.WorkspaceRoot = filepath.Dir(home)
	put(filepath.Join(home, "owner-tools", "settings"), "source owner settings")
	put(filepath.Join(home, ".claude", "auth.json"), "source-only synthetic provider identity")
	put(filepath.Join(home, ".runtimed", "identity"), "source-only synthetic runtime identity")
	checkpoint := errors.New("fixture quiesced checkpoint")
	engine.AfterPhase = func(p string) error {
		if p == "quiesced" {
			return checkpoint
		}
		return nil
	}
	if e := engine.Run(ctx, id); !errors.Is(e, checkpoint) {
		t.Fatalf("quiesce checkpoint: %v", e)
	}
	manifest := runtime.HomeManifest{Version: 2, Entries: []runtime.HomeManifestEntry{
		{Path: "workspace/app", Disposition: "separate"}, {Path: ".runtimed", Disposition: "separate"},
		{Path: "owner-tools", Disposition: "preserve"}, {Path: ".claude", Disposition: "retained", Reason: "source provider identity is not transported"},
	}}
	canonical, e := runtime.CanonicalHomeManifest(manifest)
	must(e)
	m, e := engine.Store.GetRuntimeMigration(ctx, id)
	must(e)
	m.Source.ContainerID = sql.NullString{String: "independent-backup-fixture", Valid: true}
	sourceJSON, e := json.Marshal(m.Source)
	must(e)
	_, e = engine.Store.DB().ExecContext(ctx, `UPDATE runtime_migration SET home_manifest_json=?,source_json=? WHERE sandbox_id=?`, string(canonical), string(sourceJSON), id)
	must(e)
	m, e = engine.Store.GetRuntimeMigration(ctx, id)
	must(e)
	must(f.archiveSourceHome(ctx, m))
	emptyHistory, e := runtime.ExportPrivateTaskHistory(ctx, filepath.Join(home, ".runtimed", "tasks"), nil)
	must(e)
	historyDigest, e := f.saveArchive(m, "source-history", emptyHistory)
	must(e)
	must(engine.Store.RecordMigrationHistory(ctx, id, "quiesced", historyDigest, false))
	keyPath := filepath.Join(root, "secrets.key")
	cipher, e := secrets.Load("", keyPath)
	must(e)
	engine.AfterPhase = nil
	must(engine.Run(ctx, id))
	ciphertext, nonce, e := cipher.Seal([]byte("synthetic bound supervisor credential"))
	must(e)
	_, e = engine.Store.DB().ExecContext(ctx, `UPDATE runtime_migration SET token_ciphertext=?,token_nonce=? WHERE sandbox_id=?`, ciphertext, nonce, id)
	must(e)
	put(filepath.Join(f.target, "data", "owner.db"), "new Cube workspace writes")
	taskID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	taskResult := `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","status":"succeeded"}`
	must(engine.Store.CreateTask(ctx, &store.Task{TaskID: taskID, SandboxID: id, Agent: "fixture", Prompt: "synthetic completed task"}))
	must(engine.Store.FinishTask(ctx, taskID, "succeeded", taskResult))
	targetTasks := filepath.Join(root, "target-task-history")
	put(filepath.Join(targetTasks, taskID, "result.json"), taskResult)
	put(filepath.Join(targetTasks, taskID, "events.jsonl"), "{\"type\":\"done\"}\n")
	rollbackHistory, e := runtime.ExportPrivateTaskHistory(ctx, targetTasks, []string{taskID})
	must(e)

	crashed := errors.New("power loss after rollback archive")
	engine.AfterPhase = func(p string) error {
		if p == "rollback_started" {
			return crashed
		}
		return nil
	}
	if e = engine.Rollback(ctx, id); !errors.Is(e, crashed) {
		t.Fatalf("wanted durable rollback boundary: %v", e)
	}
	m, e = engine.Store.GetRuntimeMigration(ctx, id)
	must(e)
	targetHome := filepath.Join(root, "target-home")
	must(os.MkdirAll(filepath.Join(targetHome, "workspace", "app"), 0755))
	must(os.MkdirAll(filepath.Join(targetHome, ".runtimed"), 0755))
	put(filepath.Join(targetHome, "owner-tools", "settings"), "new Cube home writes")
	homeDigest, e := f.saveHomeArchive(ctx, m, "rollback-home", func(w io.Writer) error { return runtime.ExportPrivateHome(ctx, targetHome, manifest, w) })
	must(e)
	must(engine.Store.RecordMigrationHome(ctx, id, m.Phase, homeDigest, true))
	historyDigest, e = f.saveArchive(m, "rollback-history", rollbackHistory)
	must(e)
	must(engine.Store.RecordMigrationHistory(ctx, id, m.Phase, historyDigest, true))
	engine.AfterPhase = func(p string) error {
		if p == "rollback_archived" {
			return crashed
		}
		return nil
	}
	if e = engine.Rollback(ctx, id); !errors.Is(e, crashed) {
		t.Fatalf("rollback archive checkpoint: %v", e)
	}

	backup := t.TempDir()
	// SQLite online snapshot includes WAL state. Copying only the main database
	// file while a writer is open would not establish this recovery guarantee.
	_, e = engine.Store.DB().ExecContext(ctx, `VACUUM INTO ?`, filepath.Join(backup, "sandboxd.db"))
	must(e)
	copyFixtureBackup(t, f.ArchiveDir, filepath.Join(backup, "archives"))
	copyFixtureBackup(t, home, filepath.Join(backup, "owner-home"))
	copyFixtureBackup(t, keyPath, filepath.Join(backup, "secrets.key"))
	// The fixture helper owns Store.Close; close its database now so no open
	// SQLite handle can supply any original data during the recovery.
	must(engine.Store.DB().Close())
	for _, p := range []string{home, f.target, targetHome, targetTasks, f.ArchiveDir, keyPath, dbPath, dbPath + "-wal", dbPath + "-shm"} {
		must(os.RemoveAll(p))
	}
	// There are no usable originals left. Every scope below comes from a distinct
	// copied inode in the independent backup directory.
	copyFixtureBackup(t, filepath.Join(backup, "sandboxd.db"), dbPath)
	copyFixtureBackup(t, filepath.Join(backup, "archives"), f.ArchiveDir)
	copyFixtureBackup(t, filepath.Join(backup, "owner-home"), home)
	copyFixtureBackup(t, filepath.Join(backup, "secrets.key"), keyPath)
	recovered, e := store.Open(ctx, "file:"+dbPath+"?_fk=1", "../../migrations")
	must(e)
	defer recovered.Close()
	var integrity string
	must(recovered.DB().QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	engine.Store = recovered
	f.Store = recovered
	engine.AfterPhase = nil
	m, e = recovered.GetRuntimeMigration(ctx, id)
	must(e)
	if m.Phase != "rollback_archived" || m.Binding.RuntimeID != "fresh-remote" || m.Source.ContainerID.String != "independent-backup-fixture" {
		t.Fatal("backup lost durable runtime identity")
	}
	restoredCipher, e := secrets.Load("", keyPath)
	must(e)
	plain, e := restoredCipher.Open(m.Binding.TokenCiphertext, m.Binding.TokenNonce)
	must(e)
	if string(plain) != "synthetic bound supervisor credential" {
		t.Fatal("restored binding cannot authenticate")
	}
	// Inspect is the only Docker operation expected by real RestoreSource. This
	// fixed fixture never contacts Docker or accepts arbitrary command arguments.
	inspected := []map[string]any{{"Id": "independent-backup-fixture", "State": map[string]any{"Running": false}, "Config": map[string]any{"Labels": map[string]string{"sandboxd.managed": "true"}}, "Mounts": []map[string]string{{"Source": home, "Destination": "/home/sandbox"}}}}
	encoded, e := json.Marshal(inspected[0])
	must(e)
	inspectPath := filepath.Join(root, "inspect.json")
	must(os.WriteFile(inspectPath, encoded, 0600))
	dockerPath := filepath.Join(root, "fixture-docker")
	must(os.WriteFile(dockerPath, []byte("#!/bin/sh\n[ \"$1\" = inspect ] && [ \"$4\" = independent-backup-fixture ] && [ \"$#\" = 4 ] || exit 91\ncat \"$(dirname \"$0\")/inspect.json\"\n"), 0700))
	f.Docker = &docker.Client{Bin: dockerPath}
	engine.Backend = &backupRestoreBackend{f}
	rollbackPath, e := f.artifact(m, "rollback")
	must(e)
	originalArchive, e := os.ReadFile(rollbackPath)
	must(e)
	must(os.WriteFile(rollbackPath, []byte("corrupt independent archive"), 0600))
	if e = engine.Rollback(ctx, id); e == nil {
		t.Fatal("corrupt backup accepted")
	}
	for p, want := range map[string]string{filepath.Join(app, "data", "owner.db"): "original private data", filepath.Join(home, "owner-tools", "settings"): "source owner settings"} {
		got, e := os.ReadFile(p)
		must(e)
		if string(got) != want {
			t.Fatal("failed recovery modified source before verification")
		}
	}
	must(os.WriteFile(rollbackPath, originalArchive, 0600))
	must(engine.Rollback(ctx, id))
	must(engine.Rollback(ctx, id))
	for p, want := range map[string]string{filepath.Join(app, "data", "owner.db"): "new Cube workspace writes", filepath.Join(home, "owner-tools", "settings"): "new Cube home writes", filepath.Join(home, ".claude", "auth.json"): "source-only synthetic provider identity", filepath.Join(home, ".runtimed", "identity"): "source-only synthetic runtime identity"} {
		got, e := os.ReadFile(p)
		must(e)
		if string(got) != want {
			t.Fatalf("restored data differs at %s", filepath.Base(p))
		}
	}
	historyFile, e := os.ReadFile(filepath.Join(home, ".runtimed", "tasks", taskID, "result.json"))
	must(e)
	if string(historyFile) != taskResult {
		t.Fatal("new Cube task history lost")
	}
	task, e := recovered.GetTask(ctx, taskID)
	must(e)
	if task.Status != "succeeded" || task.SandboxID != id {
		t.Fatal("task owner binding lost")
	}
	sb, e := recovered.Get(ctx, id)
	must(e)
	if sb.RuntimeProvider != "docker" || sb.ID != id || sb.AppID.String != "durable-app" || sb.Visibility != "private" {
		t.Fatal("restoration changed stable owner routing")
	}
	owner, e := recovered.GetApp(ctx, "durable-app")
	must(e)
	if owner.OwnerToken != "owner" {
		t.Fatal("restoration changed owner")
	}
	m, e = recovered.GetRuntimeMigration(ctx, id)
	must(e)
	if m.Phase != "rolled_back" {
		t.Fatal("recovery did not finish journal")
	}
}

// Test-only, offline copy preserving links/modes without following a link. It
// intentionally has no relationship to guest private export authorization.
func copyFixtureBackup(t *testing.T, source, dest string) {
	t.Helper()
	info, e := os.Lstat(source)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, e := os.Readlink(source)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Symlink(target, dest); e != nil {
			t.Fatal(e)
		}
		return
	}
	if info.IsDir() {
		if e = os.MkdirAll(dest, info.Mode().Perm()); e != nil {
			t.Fatal(e)
		}
		entries, e := os.ReadDir(source)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			copyFixtureBackup(t, filepath.Join(source, entry.Name()), filepath.Join(dest, entry.Name()))
		}
		return
	}
	if !info.Mode().IsRegular() {
		t.Fatal("unexpected fixture backup file type")
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		t.Fatal(e)
	}
	input, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer input.Close()
	output, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.Copy(output, input); e != nil {
		output.Close()
		t.Fatal(e)
	}
	if e = output.Sync(); e != nil {
		output.Close()
		t.Fatal(e)
	}
	if e = output.Close(); e != nil {
		t.Fatal(e)
	}
}
