package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestOfflineRecoverySessionRequiresExclusiveMaintenance(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native root fixture required")
	}
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, e := store.Open(ctx, "file:"+path+"?_journal=WAL&_busy_timeout=5000&_fk=1", "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	key, e := secrets.Load("", filepath.Join(dir, "controller.key"))
	if e != nil {
		t.Fatal(e)
	}
	provider := cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"}
	policy := cube.AdmissionConfig{MaxActive: 4, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}}
	if session, e := Open(ctx, path, "../../migrations", key, provider, policy); e == nil {
		session.Close()
		t.Fatal("missing daemon marker accepted")
	}
	daemon, e := maintenance.Acquire(path, false)
	if e != nil {
		t.Fatal(e)
	}
	if session, e := Open(ctx, path, "../../migrations", key, provider, policy); e == nil {
		session.Close()
		t.Fatal("recovery entered active daemon fence")
	}
	daemon.Close()
	session, e := Open(ctx, path, "../../migrations", key, provider, policy)
	if e != nil {
		t.Fatal(e)
	}
	if daemon, e := maintenance.Acquire(path, false); e == nil {
		daemon.Close()
		session.Close()
		t.Fatal("daemon entered offline session")
	}
	if e = session.Close(); e != nil {
		t.Fatal(e)
	}
	if e = session.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = session.Journal(ctx, "fixture"); e == nil {
		t.Fatal("closed session remained usable")
	}
	daemon, e = maintenance.Acquire(path, false)
	if e != nil {
		t.Fatal("closed session retained fence", e)
	}
	daemon.Close()
}
