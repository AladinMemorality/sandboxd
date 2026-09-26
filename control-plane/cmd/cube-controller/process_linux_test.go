package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestControllerProcessHelper(t *testing.T) {
	if os.Getenv("CUBE_CONTROLLER_TEST_PROCESS") != "1" {
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// Exercise the actual entrypoint, lock, SQLite, encrypted-state configuration,
// fresh root-owned worker observation, HTTP listener and graceful exit. The
// provider is a local read-only fixture; no paid model or production API calls.
func TestControllerProcessWithoutDocker(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("storage observer acceptance requires Linux root")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	st, err := store.Open(context.Background(), "file:"+dbPath+"?_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	lock, err := maintenance.Acquire(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	if err := os.WriteFile(filepath.Join(dir, "secrets.key"), []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sandboxes" {
			t.Errorf("unexpected provider operation %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer provider.Close()
	now, err := cube.ReadStorageClock()
	if err != nil {
		t.Fatal(err)
	}
	guard := &cube.StorageGuardConfig{OuterBootID: now.BootID, ObservationPath: filepath.Join(dir, "observation.json"),
		ObserverID: strings.Repeat("1", 32), WorkerMachineID: strings.Repeat("2", 32),
		ExpectedBootID: "33333333-3333-3333-3333-333333333333", InnerFSUUID: "44444444-4444-4444-4444-444444444444", OuterFSUUID: "55555555-5555-5555-5555-555555555555"}
	observation := cube.StorageObservation{Version: 1, OuterBootID: now.BootID, ObserverID: guard.ObserverID, Generation: 1,
		StartedNS: now.NS, CompletedNS: now.NS, WorkerMachineID: guard.WorkerMachineID, WorkerBootID: guard.ExpectedBootID,
		InnerFSUUID: guard.InnerFSUUID, OuterFSUUID: guard.OuterFSUUID, InnerFreeBytes: 300 * cube.StorageGiB, OuterFreeBytes: 1000 * cube.StorageGiB}
	data, _ := json.Marshal(observation)
	if err := os.WriteFile(guard.ObservationPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	templates := map[string]string{}
	for _, p := range preset.List() {
		templates[p.ID] = "tpl-reviewed"
	}
	templateJSON, _ := json.Marshal(templates)
	admissionJSON, _ := json.Marshal(cube.AdmissionConfig{MaxActive: 4, CPUCount: 2, MemoryMB: 2048, WritableDiskMB: 10240,
		StorageGuard: guard, Templates: map[string]cube.AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	vars := map[string]string{
		"CUBE_CONTROLLER_TEST_PROCESS": "1", "CUBE_CONTROLLER_ADDR": addr, "SANDBOXD_DATA_DIR": dir, "SANDBOXD_DB": dbPath,
		"SANDBOXD_MIGRATIONS": "../../migrations", "SANDBOXD_API_TOKENS": "platform=fixture",
		"SANDBOXD_PREVIEW_TOKEN_SECRETS": "v1=" + strings.Repeat("s", 32), "SANDBOXD_CUBE_ENABLED": "true",
		"SANDBOXD_CUBE_ROLLOUT": "global", "SANDBOXD_CUBE_API_URL": provider.URL, "SANDBOXD_CUBE_API_KEY": "fixture",
		"SANDBOXD_CUBE_PROXY_URL": provider.URL, "SANDBOXD_CUBE_DOMAIN": "cube.test", "SANDBOXD_CUBE_TEMPLATES": string(templateJSON),
		"SANDBOXD_CUBE_ADMISSION": string(admissionJSON), "SANDBOXD_CUBE_AGENT_RELAY_ORIGIN": "https://relay.example.test",
		"SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED": "true", "SANDBOXD_CUBE_REVERSE_EGRESS": "true",
		"SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE": "proxy-http-v1", "SANDBOXD_CUBE_BRIDGE_URL": "https://app.example.test/api/bridge",
		"SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS": "127.0.0.0/8", "SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS": "example.test",
	}
	// Deliberately no PATH and no inherited runtime/provider environment.
	cmd := exec.Command(os.Args[0], "-test.run=^TestControllerProcessHelper$")
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			b, _ := os.ReadFile(log.Name())
			t.Fatalf("controller exited: %v %s", err, b)
		default:
		}
		resp, e := client.Get("http://" + addr + "/readyz")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		b, _ := os.ReadFile(log.Name())
		t.Fatalf("controller not ready: %s", b)
	}
	if other, e := maintenance.Acquire(dbPath, false); e == nil {
		other.Close()
		t.Fatal("old controller could share database")
	}
	resp, err := client.Get("http://" + addr + "/v1/apps")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("API auth not enforced", resp.StatusCode)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown timed out")
	}
	if released, e := maintenance.Acquire(dbPath, true); e != nil {
		t.Fatal("lock retained after exit", e)
	} else {
		released.Close()
	}
}
