package recovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestPinnedOfflineRecoveryPreservesMaintenanceAndNodePolicy(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native root fixture required")
	}
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, e := store.Open(ctx, "file:"+path+"?_journal=WAL&_fk=1", "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	key, e := secrets.Load("", filepath.Join(dir, "key"))
	if e != nil {
		t.Fatal(e)
	}
	provider := cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"}
	policy := cube.AdmissionConfig{NodeID: "10.0.2.15", HostCPUMillis: 10000, HostMemoryMB: 10240, MaxActive: 4, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}}
	for _, master := range []string{"", "http://127.0.0.1:20889"} {
		if s, e := OpenPinned(ctx, path, "../../migrations", key, provider, policy, master); e == nil {
			s.Close()
			t.Fatal("missing maintenance marker accepted")
		}
	}
	daemon, e := maintenance.Acquire(path, false)
	if e != nil {
		t.Fatal(e)
	}
	if s, e := OpenPinned(ctx, path, "../../migrations", key, provider, policy, "http://127.0.0.1:20889"); e == nil {
		s.Close()
		t.Fatal("active daemon accepted")
	}
	daemon.Close()
	if s, e := Open(ctx, path, "../../migrations", key, provider, policy); e == nil {
		s.Close()
		t.Fatal("pinned admission without placement accepted")
	}
	s, e := OpenPinned(ctx, path, "../../migrations", key, provider, policy, "http://127.0.0.1:20889")
	if e != nil {
		t.Fatal(e)
	}
	if d, e := maintenance.Acquire(path, false); e == nil {
		d.Close()
		t.Fatal("daemon entered pinned session")
	}
	s.Close()
	policy.NodeID = "10.0.2.16"
	if s, e := OpenPinned(ctx, path, "../../migrations", key, provider, policy, "http://127.0.0.1:20889"); e == nil {
		s.Close()
		t.Fatal("durable worker pin changed")
	}
}

func TestRetainedIngressMustAuthenticatePlannedSupervisor(t *testing.T) {
	token := strings.Repeat("a", 64)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/status" || r.Host != "3031-replacement.cube.app" || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Cube-Traffic-Access-Token") != "private-ingress" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"runtimed":{"version":"fixture"}}`))
	}))
	defer server.Close()
	remote := &cube.Sandbox{SandboxID: "replacement"}
	if authenticateIngress(context.Background(), remote, token, "wrong", server.URL) == nil || remote.TrafficAccessToken != "" {
		t.Fatal("unauthenticated credential persisted")
	}
	if e := authenticateIngress(context.Background(), remote, token, "private-ingress", server.URL); e != nil {
		t.Fatal(e)
	}
	before := calls
	if authenticateIngress(context.Background(), remote, token, "different", server.URL) == nil || calls != before {
		t.Fatal("provider credential mismatch accepted")
	}
}
