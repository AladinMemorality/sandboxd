package store_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	guestapi "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type admissionLiveConfig struct {
	StorageGuard *cube.StorageGuardConfig `json:"storage_guard,omitempty"`
	APIURL       string                   `json:"api_url"`
	APIKey       string                   `json:"api_key"`
	ProxyURL     string                   `json:"proxy_url"`
	Domain       string                   `json:"domain"`
	TemplateID   string                   `json:"template_id"`
	WorkDir      string                   `json:"work_dir"`
	MaxActive    int                      `json:"max_active"`
}
type admissionLiveVM struct {
	AppID           string `json:"app_id"`
	SandboxID       string `json:"sandbox_id"`
	RuntimeID       string `json:"runtime_id"`
	SupervisorToken string `json:"supervisor_token"`
	TrafficToken    string `json:"traffic_token"`
}

// Explicitly opt-in, synthetic data only. Do not set this during ordinary CI.
// The operator supplies a fresh private work directory and reviewed template.
// Unknown outcomes retain the DB/private ownership evidence; cleanup touches
// only successfully returned exact VM IDs, never a provider-wide list.
func TestLiveCubeDurableAdmissionBurst(t *testing.T) {
	configPath := os.Getenv("CUBE_ADMISSION_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("opt-in disposable Cube admission fixture")
	}
	info, err := os.Lstat(configPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 64<<10 {
		t.Fatal("live configuration must be a bounded0600 regular file")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg admissionLiveConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal("invalid private live fixture configuration")
	}
	if cfg.MaxActive < 1 || cfg.MaxActive > 12 || !filepath.IsAbs(cfg.WorkDir) || cfg.Domain == "" || strings.ContainsAny(cfg.Domain, "/: ") {
		t.Fatal("invalid live fixture scope")
	}
	if err = os.Mkdir(cfg.WorkDir, 0700); err != nil {
		t.Fatal("fixture directory must be fresh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(cfg.WorkDir, "admission.db")+"?_journal=WAL&_busy_timeout=5000&_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	client, err := cube.New(cube.Config{APIURL: cfg.APIURL, APIKey: cfg.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	profile := cube.AdmissionConfig{MaxActive: cfg.MaxActive, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{cfg.TemplateID: {CPUCount: 2, MemoryMB: 2048}}, StorageGuard: cfg.StorageGuard}
	if cfg.StorageGuard != nil {
		profile.WritableDiskMB = 10240
	}
	if err = client.ConfigureAdmission(ctx, st, profile); err != nil {
		t.Fatal(err)
	}
	var runBytes [10]byte
	if _, err = rand.Read(runBytes[:]); err != nil {
		t.Fatal(err)
	}
	run := "admission-" + hex.EncodeToString(runBytes[:])
	var mu sync.Mutex
	owned := []admissionLiveVM{}
	persist := func() {
		encoded, _ := json.MarshalIndent(owned, "", "  ")
		if e := os.WriteFile(filepath.Join(cfg.WorkDir, "owned-private.json"), encoded, 0600); e != nil {
			t.Error("cannot save private cleanup identities")
		}
	}
	cleanup := func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 8*time.Minute)
		defer stop()
		mu.Lock()
		vms := append([]admissionLiveVM(nil), owned...)
		mu.Unlock()
		// Delete active VMs first, leaving a free slot for upstream's paused destroy.
		running := map[string]bool{}
		for _, vm := range vms {
			remote, e := client.Get(cleanupCtx, vm.RuntimeID)
			if e == nil {
				running[vm.RuntimeID] = remote.State == "running"
			}
		}
		sort.SliceStable(vms, func(i, j int) bool { return running[vms[i].RuntimeID] && !running[vms[j].RuntimeID] })
		cleanupOK := true
		for _, vm := range vms {
			if e := client.Delete(cleanupCtx, vm.RuntimeID); e != nil {
				cleanupOK = false
				t.Errorf("owned fixture cleanup requires review for %s: %v", vm.RuntimeID, e)
			}
		}
		var charged int
		if e := st.DB().QueryRowContext(cleanupCtx, `SELECT COALESCE(SUM(charged),0) FROM cube_admission`).Scan(&charged); e != nil || charged != 0 {
			cleanupOK = false
			t.Errorf("retained admission evidence requires review: charged=%d err=%v", charged, e)
		}
		encoded, _ := json.Marshal(map[string]any{"owned_known_vms": len(vms), "charged_remaining": charged, "cleanup_verified": cleanupOK})
		if e := os.WriteFile(filepath.Join(cfg.WorkDir, "cleanup.json"), encoded, 0600); e != nil {
			t.Error("cannot save cleanup proof")
		}
	}
	defer cleanup()
	create := func(index int) (admissionLiveVM, error) {
		vm := admissionLiveVM{AppID: fmt.Sprintf("%s-app-%d", run, index), SandboxID: fmt.Sprintf("%s-sandbox-%d", run, index)}
		var token [32]byte
		if _, e := rand.Read(token[:]); e != nil {
			return vm, e
		}
		vm.SupervisorToken = hex.EncodeToString(token[:])
		remote, e := client.Create(ctx, cube.CreateRequest{TemplateID: cfg.TemplateID, TimeoutSeconds: 3600, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": vm.SupervisorToken}, Metadata: map[string]string{"sandboxd_id": vm.SandboxID, "sandboxd_app_id": vm.AppID, "fixture": "durable-admission"}})
		if e != nil {
			return vm, e
		}
		vm.RuntimeID = remote.SandboxID
		vm.TrafficToken = remote.TrafficAccessToken
		mu.Lock()
		owned = append(owned, vm)
		persist()
		mu.Unlock()
		return vm, nil
	}
	started := time.Now()
	results := make(chan error, cfg.MaxActive)
	var workers sync.WaitGroup
	for i := 0; i < cfg.MaxActive; i++ {
		workers.Add(1)
		go func(index int) { defer workers.Done(); _, e := create(index); results <- e }(i)
	}
	workers.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatalf("owned burst allocation incomplete: %v; retain private evidence", e)
		}
	}
	burstDuration := time.Since(started)
	if _, err = create(cfg.MaxActive); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("over-budget create not refused before allocation: %v", err)
	}
	mu.Lock()
	first := owned[0]
	mu.Unlock()
	if _, err = client.Connect(ctx, first.RuntimeID, cube.ConnectRequest{TimeoutSeconds: 3600}); err != nil {
		t.Fatal("running connect consumed another slot", err)
	}
	guest, err := guestapi.NewRemoteClient(guestapi.RemoteConfig{BaseURL: cfg.ProxyURL, Host: "3031-" + first.RuntimeID + "." + cfg.Domain, Token: first.SupervisorToken, TrafficAccessToken: first.TrafficToken})
	if err != nil {
		t.Fatal(err)
	}
	attach := func() context.CancelFunc {
		channelCtx, stop := context.WithCancel(ctx)
		conn, e := guest.OpenEgressChannel(channelCtx)
		if e != nil {
			stop()
			t.Fatal(e)
		}
		go func() {
			_ = egress.RunHost(channelCtx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: first.SandboxID, Generation: first.RuntimeID}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}})
		}()
		return stop
	}
	ready := func() *guestapi.Status {
		readyCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		defer stop()
		for {
			status, e := guest.Status(readyCtx)
			if e == nil && status.Preview.Status == guestapi.PreviewReady && status.Preview.Pid > 0 {
				return status
			}
			select {
			case <-readyCtx.Done():
				t.Fatal("synthetic frontend did not become ready")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	channelStop := attach()
	defer func() { channelStop() }()
	before := ready()
	previewBody := func() []byte {
		req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(cfg.ProxyURL, "/")+"/", nil)
		if e != nil {
			t.Fatal(e)
		}
		req.Host = "3000-" + first.RuntimeID + "." + cfg.Domain
		req.Header.Set("cube-traffic-access-token", first.TrafficToken)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		defer transport.CloseIdleConnections()
		hc := http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, e := hc.Do(req)
		if e != nil {
			t.Fatal("fixture preview request failed")
		}
		defer response.Body.Close()
		body, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if e != nil || response.StatusCode != 200 || len(body) == 0 {
			t.Fatal("fixture preview not a successful nonempty response")
		}
		return body
	}
	bodyBefore := previewBody()
	if err = client.Pause(ctx, first.RuntimeID); err != nil {
		t.Fatal(err)
	}
	channelStop()
	replacement, err := create(cfg.MaxActive + 1)
	if err != nil {
		t.Fatal("verified pause did not release slot", err)
	}
	if _, err = client.Connect(ctx, first.RuntimeID, cube.ConnectRequest{}); !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("full-budget paused wake was not refused: %v", err)
	}
	if err = client.Delete(ctx, replacement.RuntimeID); err != nil {
		t.Fatal(err)
	}
	resumedAt := time.Now()
	if _, err = client.Connect(ctx, first.RuntimeID, cube.ConnectRequest{TimeoutSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
	channelStop = attach()
	after := ready()
	resumeDuration := time.Since(resumedAt)
	bodyAfter := previewBody()
	if !before.Runtimed.BootedAt.Equal(after.Runtimed.BootedAt) || before.Preview.Pid != after.Preview.Pid {
		t.Fatal("pause/resume replaced the supervisor or frontend process")
	}
	if sha256.Sum256(bodyBefore) != sha256.Sum256(bodyAfter) {
		t.Fatal("synthetic frontend response changed across pause/resume")
	}
	report := map[string]any{"storage_guard_enabled": cfg.StorageGuard != nil, "run": run, "max_active": cfg.MaxActive, "created_total": cfg.MaxActive + 1, "resource_profile": "2CPU/2GiB", "burst_seconds": burstDuration.Seconds(), "resume_ready_seconds": resumeDuration.Seconds(), "same_frontend_pid": true, "same_supervisor_boot": true, "body_sha256": fmt.Sprintf("%x", sha256.Sum256(bodyAfter)), "timing_conditions": "concurrent disposable validation, not a performance benchmark"}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if err = os.WriteFile(filepath.Join(cfg.WorkDir, "result.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
