package migration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Real filesystem copies and SQLite transactions, with only the hypervisor
// lifecycle replaced. Hidden owner files and post-cutover writes are exercised.
type fixtureBackend struct {
	*OfflineBackend
	source, target string
	failCreate     bool
	creates        int
}

func (f *fixtureBackend) ValidateRollback(context.Context, *store.RuntimeMigration) error { return nil }
func (f *fixtureBackend) PrepareRollback(ctx context.Context, m *store.RuntimeMigration) error {
	if m.RollbackRecreate {
		return f.Store.RecordRetainedDocker(ctx, m.SandboxID, retainedDockerName(m.SandboxID))
	}
	return nil
}
func (f *fixtureBackend) StopSource(context.Context, *store.RuntimeMigration) error { return nil }
func (f *fixtureBackend) ArchiveSource(_ context.Context, m *store.RuntimeMigration) (string, error) {
	data, e := runtime.ExportPrivateWorkspace(f.source)
	if e != nil {
		return "", e
	}
	return f.saveArchive(m, "source", data)
}
func (f *fixtureBackend) StageTarget(ctx context.Context, m *store.RuntimeMigration) error {
	f.creates++
	if e := f.Store.PrepareMigrationTargetCredential(ctx, m.SandboxID, []byte("encrypted-supervisor"), []byte("nonce")); e != nil {
		return e
	}
	if f.failCreate {
		return errors.New("simulated response lost after remote creation")
	}
	return f.Store.SaveMigrationTarget(ctx, m.SandboxID, &store.RuntimeBinding{RuntimeID: "fresh-remote", TokenCiphertext: []byte("encrypted-complete"), TokenNonce: []byte("nonce")})
}
func (f *fixtureBackend) ImportTarget(_ context.Context, m *store.RuntimeMigration) error {
	data, e := f.readArchive(m, "source", m.ArchiveSHA256)
	if e != nil {
		return e
	}
	return runtime.InstallPrivateWorkspace(f.target, data)
}
func (f *fixtureBackend) VerifyTarget(_ context.Context, m *store.RuntimeMigration) error {
	data, e := runtime.ExportPrivateWorkspace(f.target)
	if e != nil {
		return e
	}
	digest, e := runtime.PrivateWorkspaceDigest(data)
	if e != nil {
		return e
	}
	if digest != m.ArchiveSHA256 {
		return errors.New("target checksum mismatch")
	}
	return nil
}
func (f *fixtureBackend) ReadyTarget(context.Context, *store.RuntimeMigration) error { return nil }
func (f *fixtureBackend) ArchiveTarget(_ context.Context, m *store.RuntimeMigration) (string, error) {
	data, e := runtime.ExportPrivateWorkspace(f.target)
	if e != nil {
		return "", e
	}
	return f.saveArchive(m, "rollback", data)
}
func (f *fixtureBackend) RestoreSource(_ context.Context, m *store.RuntimeMigration) error {
	data, e := f.readArchive(m, "rollback", m.RollbackSHA256)
	if e != nil {
		return e
	}
	return runtime.InstallPrivateWorkspace(f.source, data)
}
func (f *fixtureBackend) PauseTarget(context.Context, *store.RuntimeMigration) error { return nil }

func fixture(t *testing.T) (*Engine, *fixtureBackend, string, string) {
	return fixtureID(t, "stable-id")
}
func fixtureID(t *testing.T, id string) (*Engine, *fixtureBackend, string, string) {
	t.Helper()
	root := t.TempDir()
	db := filepath.Join(root, "sandboxd.db")
	st, e := store.Open(context.Background(), "file:"+db+"?_fk=1", "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	app := &store.App{ID: "durable-app", OwnerToken: "owner", Name: "fixture"}
	if e = st.CreateApp(context.Background(), app); e != nil {
		t.Fatal(e)
	}
	if e = st.Create(context.Background(), &store.Sandbox{ID: id, Status: "stopped", Image: "original", AppID: sql.NullString{String: app.ID, Valid: true}, Ports: []int{3000}, Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, dir := range []string{source, target, filepath.Join(source, ".git"), filepath.Join(source, "data")} {
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
	}
	for name, data := range map[string]string{".env": "OWNER_SECRET=private", ".git/HEAD": "ref: refs/heads/main", "data/owner.db": "original private data", "index.js": "export default 1"} {
		if e = os.WriteFile(filepath.Join(source, name), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = st.BeginRuntimeMigration(context.Background(), id, "react-vite", "trusted-template", "cube.test"); e != nil {
		t.Fatal(e)
	}
	backend := &fixtureBackend{OfflineBackend: &OfflineBackend{Store: st, ArchiveDir: filepath.Join(root, "archives")}, source: source, target: target}
	return &Engine{Store: st, Backend: backend}, backend, id, db
}

func TestMigrationResumeEveryCommittedPhaseAndRollbackNewWrites(t *testing.T) {
	for _, phase := range []string{"quiesced", "archived", "staged", "imported", "verified", "complete"} {
		t.Run(phase, func(t *testing.T) {
			engine, backend, id, db := fixture(t)
			ctx := context.Background()
			crash := errors.New("crash")
			engine.AfterPhase = func(got string) error {
				if got == phase {
					return crash
				}
				return nil
			}
			if err := engine.Run(ctx, id); !errors.Is(err, crash) {
				t.Fatalf("wanted crash at %s, got %v", phase, err)
			}
			// A new database connection reads the durable checkpoint and identities.
			recovered, err := store.Open(ctx, "file:"+db+"?_fk=1", "../../migrations")
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			engine.Store = recovered
			backend.Store = recovered
			engine.AfterPhase = nil
			if err = engine.Run(ctx, id); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{".env", ".git/HEAD", "data/owner.db"} {
				source, _ := os.ReadFile(filepath.Join(backend.source, name))
				target, err := os.ReadFile(filepath.Join(backend.target, name))
				if err != nil || string(source) != string(target) {
					t.Fatalf("private file lost: %s %v", name, err)
				}
			}
			if err = os.WriteFile(filepath.Join(backend.target, "data", "owner.db"), []byte("new Cube writes"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = engine.Rollback(ctx, id); err != nil {
				t.Fatal(err)
			}
			restored, err := os.ReadFile(filepath.Join(backend.source, "data", "owner.db"))
			if err != nil || string(restored) != "new Cube writes" {
				t.Fatalf("rollback lost target writes: %s %v", restored, err)
			}
			sb, err := recovered.Get(ctx, id)
			if err != nil || sb.RuntimeProvider != "docker" || sb.AppID.String != "durable-app" || sb.Ports[0] != 3000 {
				t.Fatalf("stable identity changed: %+v %v", sb, err)
			}
			m, err := recovered.GetRuntimeMigration(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			original, err := backend.readArchive(m, "source", m.ArchiveSHA256)
			if err != nil || len(original) == 0 {
				t.Fatal("original recovery archive missing", err)
			}
		})
	}
}

func TestRollbackResumeEveryCommittedPhase(t *testing.T) {
	for _, phase := range []string{"rollback_started", "rollback_archived", "rollback_restored", "rolled_back"} {
		t.Run(phase, func(t *testing.T) {
			engine, backend, id, _ := fixture(t)
			ctx := context.Background()
			if err := engine.Run(ctx, id); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(backend.target, "after-cutover"), []byte("preserve me"), 0600); err != nil {
				t.Fatal(err)
			}
			crash := errors.New("crash")
			engine.AfterPhase = func(got string) error {
				if got == phase {
					return crash
				}
				return nil
			}
			if err := engine.Rollback(ctx, id); !errors.Is(err, crash) {
				t.Fatal("missing injected failure", err)
			}
			engine.AfterPhase = nil
			if err := engine.Rollback(ctx, id); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(backend.source, "after-cutover"))
			if err != nil || string(data) != "preserve me" {
				t.Fatal("rollback lost writes", err)
			}
		})
	}
}

func TestAmbiguousCreateNeverAllocatesTwice(t *testing.T) {
	engine, backend, id, _ := fixture(t)
	ctx := context.Background()
	backend.failCreate = true
	if err := engine.Run(ctx, id); err == nil {
		t.Fatal("lost response hidden")
	}
	if err := engine.Run(ctx, id); err == nil {
		t.Fatal("uncertain allocation retried")
	}
	if backend.creates != 1 {
		t.Fatalf("allocated %d times", backend.creates)
	}
	pending, err := engine.Store.HasIncompleteRuntimeMigrations(ctx)
	if err != nil || !pending {
		t.Fatal("unsafe daemon restart permitted", err)
	}
}

func TestCorruptRecoveryArchiveNeverChangesProvider(t *testing.T) {
	engine, backend, id, _ := fixture(t)
	ctx := context.Background()
	if err := engine.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	engine.AfterPhase = func(phase string) error {
		if phase == "rollback_archived" {
			return errors.New("stop")
		}
		return nil
	}
	if err := engine.Rollback(ctx, id); err == nil {
		t.Fatal("missing crash")
	}
	m, err := engine.Store.GetRuntimeMigration(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := backend.artifact(m, "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(archive, []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	engine.AfterPhase = nil
	if err = engine.Rollback(ctx, id); err == nil {
		t.Fatal("corruption accepted")
	}
	sb, err := engine.Store.Get(ctx, id)
	if err != nil || sb.RuntimeProvider != "cube" {
		t.Fatal("provider changed before verified data", err)
	}
	original, err := os.ReadFile(filepath.Join(backend.source, "data", "owner.db"))
	if err != nil || string(original) != "original private data" {
		t.Fatal("retained source overwritten", err)
	}
}

func TestChangedRuntimeConfigRollbackRequiresRecreation(t *testing.T) {
	engine, _, id, _ := fixture(t)
	ctx := context.Background()
	if err := engine.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.CreateAppConfig(ctx, &store.AppConfig{ID: "new-config", AppID: "durable-app", Key: "PUBLIC_VALUE", ValuePlaintext: sql.NullString{String: "new value", Valid: true}, AccessPolicy: "runtime_access"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Rollback(ctx, id); err != nil {
		t.Fatal(err)
	}
	m, err := engine.Store.GetRuntimeMigration(ctx, id)
	if err != nil || m.Phase != "rolled_back" || !m.RollbackRecreate || m.RetainedDockerName == "" {
		t.Fatal("recreation not journaled", err)
	}
	sb, err := engine.Store.Get(ctx, id)
	if err != nil || sb.ContainerID.Valid || sb.Status != "stopped" {
		t.Fatal("stale retained Docker environment could resume", err)
	}
}

func TestRollbackConfigDriftAfterCheckpointRemainsRecoverable(t *testing.T) {
	engine, _, id, _ := fixture(t)
	ctx := context.Background()
	if e := engine.Run(ctx, id); e != nil {
		t.Fatal(e)
	}
	engine.AfterPhase = func(phase string) error {
		if phase == "rollback_started" {
			return errors.New("power loss")
		}
		return nil
	}
	if e := engine.Rollback(ctx, id); e == nil {
		t.Fatal("missing failpoint")
	}
	if e := engine.Store.CreateAppConfig(ctx, &store.AppConfig{ID: "changed-during-recovery", AppID: "durable-app", Key: "PUBLIC_VALUE", ValuePlaintext: sql.NullString{String: "unreviewed", Valid: true}, AccessPolicy: "runtime_access"}); e != nil {
		t.Fatal(e)
	}
	engine.AfterPhase = nil
	if e := engine.Rollback(ctx, id); e == nil {
		t.Fatal("config drift ignored")
	}
	current, e := engine.Store.Get(ctx, id)
	if e != nil || current.RuntimeProvider != "cube" {
		t.Fatal("provider changed after drift", e)
	}
	m, e := engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil || m.Phase != "rollback_started" {
		t.Fatal("recovery phase lost", e)
	}
}
