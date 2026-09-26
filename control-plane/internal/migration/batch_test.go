package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type parallelFixture struct {
	*fixtureBackend
	entered  chan struct{}
	release  <-chan struct{}
	creating *atomic.Int32
	overlap  *atomic.Bool
	paused   atomic.Bool
}

func (f *parallelFixture) ArchiveSource(ctx context.Context, m *store.RuntimeMigration) (string, error) {
	f.entered <- struct{}{}
	select {
	case <-f.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return f.fixtureBackend.ArchiveSource(ctx, m)
}
func (f *parallelFixture) StageTarget(ctx context.Context, m *store.RuntimeMigration) error {
	if f.creating.Add(1) != 1 {
		f.overlap.Store(true)
	}
	defer f.creating.Add(-1)
	time.Sleep(5 * time.Millisecond)
	f.creates++
	if err := f.Store.PrepareMigrationTargetCredential(ctx, m.SandboxID, []byte("encrypted"), []byte("nonce")); err != nil {
		return err
	}
	return f.Store.SaveMigrationTarget(ctx, m.SandboxID, &store.RuntimeBinding{RuntimeID: "remote-" + m.SandboxID, TokenCiphertext: []byte("encrypted"), TokenNonce: []byte("nonce")})
}
func (f *parallelFixture) PauseTarget(context.Context, *store.RuntimeMigration) error {
	f.paused.Store(true)
	return nil
}

func TestBatchSharedSQLiteParallelCopiesAndIndependentFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	st, err := store.Open(ctx, "file:"+filepath.Join(root, "state.db")+"?_journal=WAL&_busy_timeout=5000&_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	creating := new(atomic.Int32)
	overlap := new(atomic.Bool)
	runs := []BatchRun{}
	backends := []*parallelFixture{}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("project-%d", i)
		app := &store.App{ID: "app-" + id, OwnerToken: "owner", Name: id}
		if err = st.CreateApp(ctx, app); err != nil {
			t.Fatal(err)
		}
		if err = st.Create(ctx, &store.Sandbox{ID: id, Status: "stopped", Image: "source", AppID: sql.NullString{String: app.ID, Valid: true}, Ports: []int{3000}, Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		source, target := filepath.Join(root, id, "source"), filepath.Join(root, id, "target")
		for _, dir := range []string{source, target} {
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err = os.WriteFile(filepath.Join(source, "owner.txt"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		if err = st.BeginRuntimeMigration(ctx, id, "react-vite", "template", "cube.test"); err != nil {
			t.Fatal(err)
		}
		backend := &parallelFixture{fixtureBackend: &fixtureBackend{OfflineBackend: &OfflineBackend{Store: st, ArchiveDir: filepath.Join(root, "archives")}, source: source, target: target}, entered: entered, release: release, creating: creating, overlap: overlap}
		backends = append(backends, backend)
		runs = append(runs, BatchRun{SandboxID: id, Engine: Engine{Store: st, Backend: backend, StopAfterImport: true}})
	}
	done := make(chan error, 1)
	go func() { done <- RunBatch(ctx, runs) }()
	// Every copy must start before any can finish; a serial runner cannot pass.
	for range runs {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("copies did not overlap")
		}
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if overlap.Load() {
		t.Fatal("provider creation overlapped")
	}
	for i, run := range runs {
		journal, e := st.GetRuntimeMigration(ctx, run.SandboxID)
		if e != nil || journal.Phase != "imported" {
			t.Fatal(journal, e)
		}
		source, e := st.Get(ctx, run.SandboxID)
		if e != nil || source.RuntimeProvider != "docker" {
			t.Fatal(source, e)
		}
		data, e := os.ReadFile(filepath.Join(backends[i].target, "owner.txt"))
		if e != nil || string(data) != run.SandboxID {
			t.Fatal("cross-project copy", e)
		}
		runs[i].Engine.StopAfterImport = false
	}
	// One failed verification must neither publish that project nor cancel its
	// siblings halfway through provider operations.
	if err = os.WriteFile(filepath.Join(backends[0].target, "owner.txt"), []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = RunBatch(ctx, runs); err == nil {
		t.Fatal("corrupt target accepted")
	}
	for i, run := range runs {
		journal, e := st.GetRuntimeMigration(ctx, run.SandboxID)
		if e != nil {
			t.Fatal(e)
		}
		want := "complete"
		if i == 0 {
			want = "imported"
		}
		if journal.Phase != want || backends[i].creates != 1 || backends[i].paused.Load() != (i != 0) {
			t.Fatalf("wrong independent outcome %d: %s", i, journal.Phase)
		}
	}
	if err = os.WriteFile(filepath.Join(backends[0].target, "owner.txt"), []byte(runs[0].SandboxID), 0600); err != nil {
		t.Fatal(err)
	}
	if err = RunBatch(ctx, runs[:1]); err != nil {
		t.Fatal(err)
	}
	if backends[0].creates != 1 {
		t.Fatal("resume duplicated creation")
	}
}

func TestBatchRefusesDuplicateOrOversizedWorkBeforeMutation(t *testing.T) {
	e, _, id, _ := fixture(t)
	run := BatchRun{SandboxID: id, Engine: *e}
	for _, runs := range [][]BatchRun{nil, {run, run}, {run, run, run, run, run}} {
		if err := RunBatch(context.Background(), runs); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
	journal, err := e.Store.GetRuntimeMigration(context.Background(), id)
	if err != nil || journal.Phase != "planned" {
		t.Fatal("invalid batch mutated journal", err)
	}
}
