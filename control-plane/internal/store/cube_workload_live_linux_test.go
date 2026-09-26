package store_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	guestapi "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

//go:embed testdata/cube-workload.cjs
var workloadScript []byte

const workloadBytes = 512 << 20

type workloadSample struct {
	Nonce   string                        `json:"nonce"`
	PID     int                           `json:"pid"`
	Started bool                          `json:"started"`
	Active  bool                          `json:"active"`
	Bytes   int64                         `json:"bytes"`
	Pages   int64                         `json:"pages"`
	Cycles  uint64                        `json:"cycles"`
	RSS     int64                         `json:"rss"`
	CPU     struct{ User, System uint64 } `json:"cpu"`
	Faults  struct{ Minor, Major uint64 } `json:"faults"`
}
type workloadMemory struct {
	Host            string  `json:"host"`
	AvailableKiB    uint64  `json:"available_kib"`
	SwapFreeKiB     uint64  `json:"swap_free_kib"`
	OOMKills        uint64  `json:"oom_kills"`
	SwapTotalKiB    uint64  `json:"swap_total_kib"`
	PageFaults      uint64  `json:"page_faults"`
	MajorFaults     uint64  `json:"major_faults"`
	CPUUserTicks    uint64  `json:"cpu_user_ticks"`
	CPUSystemTicks  uint64  `json:"cpu_system_ticks"`
	Runnable        uint64  `json:"runnable"`
	MemoryFullAvg10 float64 `json:"memory_full_avg10"`
}
type workloadHostSample struct {
	At     time.Time      `json:"at"`
	Outer  workloadMemory `json:"outer"`
	Worker workloadMemory `json:"worker"`
}

func parseWorkloadMemory(raw []byte) (workloadMemory, error) {
	var m workloadMemory
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 4 {
		return m, errors.New("missing host memory evidence")
	}
	m.Host = strings.TrimSpace(lines[0])
	found := map[string]bool{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key == "cpu" && len(fields) >= 5 {
			m.CPUUserTicks, _ = strconv.ParseUint(fields[1], 10, 64)
			m.CPUSystemTicks, _ = strconv.ParseUint(fields[3], 10, 64)
			continue
		}
		if key == "full" && len(fields) >= 2 && strings.HasPrefix(fields[1], "avg10=") {
			m.MemoryFullAvg10, _ = strconv.ParseFloat(strings.TrimPrefix(fields[1], "avg10="), 64)
			continue
		}
		if key == "SwapTotal" || key == "pgfault" || key == "pgmajfault" || key == "procs_running" {
			n, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return m, errors.New("invalid extended host metric")
			}
			switch key {
			case "SwapTotal":
				m.SwapTotalKiB = n
			case "pgfault":
				m.PageFaults = n
			case "pgmajfault":
				m.MajorFaults = n
			case "procs_running":
				m.Runnable = n
			}
			continue
		}
		if key != "MemAvailable" && key != "SwapFree" && key != "oom_kill" {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || found[key] {
			return m, errors.New("invalid host memory evidence")
		}
		if key != "oom_kill" && (len(fields) != 3 || fields[2] != "kB") {
			return m, errors.New("invalid host memory units")
		}
		found[key] = true
		switch key {
		case "MemAvailable":
			m.AvailableKiB = n
		case "SwapFree":
			m.SwapFreeKiB = n
		case "oom_kill":
			m.OOMKills = n
		}
	}
	if len(found) != 3 || m.Host == "" {
		return m, errors.New("incomplete host memory evidence")
	}
	return m, nil
}

func workloadSSH(ctx context.Context, command string) ([]byte, error) {
	// Fixed operator-owned endpoint/identity. No guest input or arbitrary config command.
	cmd := exec.CommandContext(ctx, "ssh", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-p", "20222", "-oBatchMode=yes", "-oConnectTimeout=5", "-oStrictHostKeyChecking=yes", "-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "root@127.0.0.1", command)
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("read-only worker metric/inventory command failed")
	}
	return out, nil
}

// Hold the same worker-side flock as PostgreSQL/reload acceptance until cleanup.
// SSH stdin closure releases the lock if this coordinator exits unexpectedly.
func workloadWorkerLock() (func(), error) {
	cmd := exec.Command("ssh", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-p", "20222", "-oBatchMode=yes", "-oConnectTimeout=5", "-oServerAliveInterval=5", "-oServerAliveCountMax=1", "-oStrictHostKeyChecking=yes", "-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "root@127.0.0.1", "exec 9>/run/lock/cube-operator-acceptance.lock; flock -n 9 || exit 2; echo WORKLOAD_LOCK_HELD; cat >/dev/null")
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		return nil, errors.New("worker operator lock unavailable")
	}
	line := make(chan string, 1)
	go func() { text, _ := bufio.NewReader(io.LimitReader(output, 128)).ReadString('\n'); line <- text }()
	release := func() {
		input.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	select {
	case text := <-line:
		if text == "WORKLOAD_LOCK_HELD\n" {
			return release, nil
		}
	case <-time.After(10 * time.Second):
	}
	release()
	return nil, errors.New("another operator fixture owns worker or SSH unavailable")
}

func workloadMetrics(ctx context.Context) (workloadHostSample, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sample := workloadHostSample{At: time.Now().UTC()}
	var local bytes.Buffer
	for _, p := range []string{"/proc/sys/kernel/hostname", "/proc/meminfo", "/proc/vmstat", "/proc/stat", "/proc/pressure/memory"} {
		b, e := os.ReadFile(p)
		if e != nil {
			return sample, e
		}
		local.Write(b)
	}
	var err error
	sample.Outer, err = parseWorkloadMemory(local.Bytes())
	if err != nil {
		return sample, err
	}
	raw, err := workloadSSH(ctx, "cat /proc/sys/kernel/hostname /proc/meminfo /proc/vmstat /proc/stat /proc/pressure/memory")
	if err != nil {
		return sample, err
	}
	sample.Worker, err = parseWorkloadMemory(raw)
	if err != nil {
		return sample, err
	}
	if sample.Worker.Host != "baarcha-cube-worker-01" || sample.Outer.Host == sample.Worker.Host {
		return sample, errors.New("fixture must run on outer host against reviewed worker")
	}
	return sample, nil
}
func workloadMemorySafe(before, now workloadHostSample, initial bool) error {
	workerFloor, outerFloor := uint64(6<<20), uint64(8<<20)
	if initial {
		workerFloor = 12 << 20
		outerFloor = 12 << 20
	}
	if now.Worker.AvailableKiB < workerFloor || now.Outer.AvailableKiB < outerFloor {
		return errors.New("available memory below fixture safety floor")
	}
	if now.Worker.SwapTotalKiB > now.Worker.SwapFreeKiB || now.Outer.SwapTotalKiB > now.Outer.SwapFreeKiB {
		return errors.New("host swap use detected")
	}
	if now.Worker.OOMKills != before.Worker.OOMKills || now.Outer.OOMKills != before.Outer.OOMKills {
		return errors.New("host OOM counter changed")
	}
	return nil
}
func workloadEgressPolicy() egress.Policy {
	// IPv6 is unsupported/denied by the transport itself, not a valid protected prefix.
	return egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}
}

func workloadArchive() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"sandbox.yaml", []byte("version: 1\nweb:\n  command: node --expose-gc workload.cjs\n  port: 3000\n  health_path: /health\nbuild:\n  command: ''\n")},
		{"workload.cjs", workloadScript},
	} {
		h := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		h.SetMode(0644)
		w, e := zw.CreateHeader(h)
		if e != nil {
			return nil, e
		}
		if _, e = w.Write(file.data); e != nil {
			return nil, e
		}
	}
	if e := zw.Close(); e != nil {
		return nil, e
	}
	return buf.Bytes(), nil
}
func waitWorkloadSupervisor(ctx context.Context, guest *guestapi.Client) (*guestapi.Status, error) {
	ready, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for ready.Err() == nil {
		status, err := guest.Status(ready)
		if err == nil && !status.Runtimed.BootedAt.IsZero() {
			if status.ActiveTask != nil {
				return nil, errors.New("fresh fixture unexpectedly has an active task")
			}
			return status, nil
		}
		select {
		case <-ready.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, errors.New("authenticated fixture supervisor did not become ready")
}

func workloadResponse(ctx context.Context, hc *http.Client, cfg admissionLiveConfig, vm admissionLiveVM, method, path string) (workloadSample, time.Duration, error) {
	var s workloadSample
	req, e := http.NewRequestWithContext(ctx, method, cfg.ProxyURL+path, nil)
	if e != nil {
		return s, 0, e
	}
	req.Host = "3000-" + vm.RuntimeID + "." + cfg.Domain
	req.Header.Set("cube-traffic-access-token", vm.TrafficToken)
	start := time.Now()
	r, e := hc.Do(req)
	if e != nil {
		return s, time.Since(start), fmt.Errorf("synthetic preview transport failed: %w", e)
	}
	defer r.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(r.Body, 4097))
	if e != nil || r.StatusCode != 200 || len(raw) > 4096 || json.Unmarshal(raw, &s) != nil {
		return s, time.Since(start), fmt.Errorf("invalid synthetic preview response HTTP %d bytes %d", r.StatusCode, len(raw))
	}
	return s, time.Since(start), nil
}
func validateWorkloadSample(old, s workloadSample) error {
	if !s.Started || !s.Active || s.Bytes != workloadBytes || s.Pages != (workloadBytes/4096)*90 || s.RSS < workloadBytes || s.PID <= 0 || len(s.Nonce) != 32 {
		return errors.New("workload memory not resident/touched")
	}
	if old.Nonce != "" && (old.Nonce != s.Nonce || old.PID != s.PID || s.Cycles <= old.Cycles || s.CPU.User+s.CPU.System <= old.CPU.User+old.CPU.System) {
		return errors.New("workload restarted or made no CPU progress")
	}
	return nil
}

func validWorkloadSlots(slots int) bool { return slots == 4 || slots == 6 || slots == 8 || slots == 12 }

// A reviewed, explicitly scheduled operator fixture. Never enabled in ordinary CI.
// Run on the outer host; inner worker must contain no other guest or template job.
func TestLiveCubeAppWorkload(t *testing.T) {
	path := os.Getenv("CUBE_WORKLOAD_LIVE_CONFIG")
	if path == "" {
		t.Skip("opt-in synthetic4/6/8/12-app workload; requires exclusive worker handoff")
	}
	if e := workloadEgressPolicy().Validate(); e != nil {
		t.Fatal(e)
	}
	if os.Geteuid() != 0 {
		t.Fatal("native outer-host root required")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 64<<10 {
		t.Fatal("bounded private0600 config required")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != 0 {
		t.Fatal("configuration must be root-owned")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var config struct {
		admissionLiveConfig
		PausedBaseline *workloadPausedBaseline `json:"paused_baseline,omitempty"`
		Profile        string                  `json:"profile"`
	}
	cfg := &config.admissionLiveConfig
	if json.Unmarshal(raw, &config) != nil || !validWorkloadSlots(cfg.MaxActive) || cfg.APIURL != "http://127.0.0.1:20300" || cfg.ProxyURL != "http://127.0.0.1:20080" || cfg.Domain != "cube.app" || !strings.HasPrefix(cfg.WorkDir, "/opt/baarcha-bench/cube-workload-") || filepath.Clean(cfg.WorkDir) != cfg.WorkDir || cfg.TemplateID == "" {
		t.Fatal("exact fresh-worker endpoint, private unique stage, and4/6/8/12-slot config required")
	}
	resources, err := workloadProfile(config.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if !cube.BenchmarkAdmissionBuild && (cfg.MaxActive > 4 || resources.CPUCount != 2 || resources.MemoryMB != 2048) {
		t.Fatal("capacity/profile requires cube_workload_benchmark test build")
	}
	if config.PausedBaseline != nil {
		if e = config.PausedBaseline.validate(); e != nil {
			t.Fatal(e)
		}
	}
	if cfg.StorageGuard == nil {
		t.Fatal("benchmark requires the real storage guard")
	}
	if e = os.Mkdir(cfg.WorkDir, 0700); e != nil {
		t.Fatal("workload stage must be fresh")
	}
	lock, e := os.OpenFile("/opt/baarcha-bench/cube-workload-operator.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal("another workload coordinator is active")
	}
	releaseWorker, e := workloadWorkerLock()
	if e != nil {
		t.Fatal(e)
	}
	defer releaseWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	hc := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	inventory := func(ctx context.Context) error {
		req, e := http.NewRequestWithContext(ctx, "GET", cfg.APIURL+"/sandboxes", nil)
		if e != nil {
			return e
		}
		req.Header.Set("X-API-Key", cfg.APIKey)
		r, e := hc.Do(req)
		if e != nil {
			return errors.New("provider inventory unavailable")
		}
		defer r.Body.Close()
		b, e := io.ReadAll(io.LimitReader(r.Body, 65537))
		var rows []json.RawMessage
		if e != nil || r.StatusCode != 200 || len(b) > 65536 || json.Unmarshal(b, &rows) != nil || rows == nil || validateWorkloadInventory(rows, config.PausedBaseline) != nil {
			return errors.New("provider inventory differs from reviewed paused baseline or empty inventory")
		}
		return nil
	}
	if e = inventory(ctx); e != nil {
		t.Fatal(e)
	}
	all, e := workloadSSH(ctx, "cubemastercli -a 127.0.0.1 list --all --wide")
	if e != nil {
		t.Fatal(e)
	}
	expectedCount := "0"
	if config.PausedBaseline != nil {
		expectedCount = "1"
		if !strings.Contains(string(all), config.PausedBaseline.RuntimeID) {
			t.Fatal("paused baseline missing from all-state inventory")
		}
	}
	zero := false
	for _, line := range strings.Split(string(all), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "SANDBOX_COUNT" {
			if f[1] != expectedCount || zero {
				t.Fatal("nonempty all-state inventory")
			}
			zero = true
		}
	}
	if !zero {
		t.Fatal("all-state inventory proof missing")
	}
	before, e := workloadMetrics(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = workloadMemorySafe(before, before, true); e != nil {
		t.Fatal(e)
	}
	report := map[string]any{"production_accepted": false, "load_seconds": 45, "per_guest_bytes": workloadBytes, "guests": cfg.MaxActive, "before": before, "timing_conditions": "bounded synthetic workload, not a general capacity benchmark"}
	save := func(name string, value any) {
		b, e := json.MarshalIndent(value, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(cfg.WorkDir, name), b, 0600)
		}
		if e != nil {
			t.Error("fixture evidence write failed")
		}
	}
	report["paused_baseline"] = config.PausedBaseline
	report["profile"] = config.Profile
	report["resources"] = resources
	report["benchmark_build"] = cube.BenchmarkAdmissionBuild
	save("result.json", report)
	st, e := store.Open(ctx, filepath.Join(cfg.WorkDir, "admission.db")+"?_journal=WAL&_busy_timeout=5000&_fk=1", "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	client, e := cube.New(cube.Config{APIURL: cfg.APIURL, APIKey: cfg.APIKey})
	if e != nil {
		t.Fatal(e)
	}
	admission := cube.AdmissionConfig{MaxActive: cfg.MaxActive, CPUCount: resources.CPUCount, MemoryMB: resources.MemoryMB, Templates: map[string]cube.AdmissionResources{cfg.TemplateID: resources}, StorageGuard: cfg.StorageGuard}
	if cfg.StorageGuard != nil {
		admission.WritableDiskMB = 10240
		report["storage_guard_enabled"] = true
		report["storage_guard_contract"] = cfg.StorageGuard.Contract()
		report["storage_outer_boot_id"] = cfg.StorageGuard.OuterBootID
		report["storage_worker_boot_id"] = cfg.StorageGuard.ExpectedBootID
	} else {
		report["storage_guard_enabled"] = false
	}
	if e = client.ConfigureAdmission(ctx, st, admission); e != nil {
		t.Fatal(e)
	}
	var run [12]byte
	if _, e = rand.Read(run[:]); e != nil {
		t.Fatal(e)
	}
	prefix := "workload-" + hex.EncodeToString(run[:])
	owned := []admissionLiveVM{}
	guests := []*guestapi.Client{}
	channels := []context.CancelFunc{}
	defer func() {
		// Separate finite cleanup context; workload itself stops by80s from activation even if cleanup fails.
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		for _, close := range channels {
			close()
		}
		ok := true
		for _, vm := range owned {
			if err := client.Delete(cleanup, vm.RuntimeID); err != nil {
				ok = false
				t.Errorf("owned guest cleanup requires review: %s", vm.RuntimeID)
			}
		}
		var charged int
		if err := st.DB().QueryRowContext(cleanup, "SELECT COALESCE(SUM(charged),0) FROM cube_admission").Scan(&charged); err != nil || charged != 0 {
			ok = false
			t.Error("charged admission remains; retain private evidence")
		}
		if err := inventory(cleanup); err != nil {
			ok = false
			t.Error("provider inventory not empty after owned cleanup")
		}
		after, err := workloadMetrics(cleanup)
		if err != nil {
			ok = false
			t.Error(err)
		} else {
			report["after"] = after
			if err = workloadMemorySafe(before, after, false); err != nil {
				ok = false
				t.Error(err)
			}
		}
		report["cleanup_verified"] = ok
		report["charged_remaining"] = charged
		save("result.json", report)
	}()
	monitor, e := startWorkloadMonitor(ctx, cancel, cfg.WorkDir, before)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		monitor.stop()
		report["monitor"] = monitor.result()
		if monitor.abort != "" {
			t.Error("workload aborted by safety monitor:", monitor.abort)
		}
	}()
	monitor.phase("provisioning")
	archive, e := workloadArchive()
	if e != nil {
		t.Fatal(e)
	}
	create := func(i int) (admissionLiveVM, error) {
		vm := admissionLiveVM{AppID: fmt.Sprintf("%s-app-%d", prefix, i), SandboxID: fmt.Sprintf("%s-sandbox-%d", prefix, i)}
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			return vm, err
		}
		vm.SupervisorToken = hex.EncodeToString(token[:])
		r, err := client.Create(ctx, cube.CreateRequest{TemplateID: cfg.TemplateID, TimeoutSeconds: 600, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": vm.SupervisorToken}, Metadata: map[string]string{"sandboxd_id": vm.SandboxID, "sandboxd_app_id": vm.AppID, "fixture": "bounded-app-workload"}})
		if err != nil {
			return vm, err
		}
		vm.RuntimeID = r.SandboxID
		vm.TrafficToken = r.TrafficAccessToken
		owned = append(owned, vm)
		save("owned-private.json", owned)
		return vm, nil
	}
	for i := 0; i < cfg.MaxActive; i++ {
		vm, err := create(i)
		if err != nil {
			t.Fatal("synthetic allocation failed; retain admission evidence", err)
		}
		guest, err := guestapi.NewRemoteClient(guestapi.RemoteConfig{BaseURL: cfg.ProxyURL, Host: "3031-" + vm.RuntimeID + "." + cfg.Domain, Token: vm.SupervisorToken, TrafficAccessToken: vm.TrafficToken})
		if err != nil {
			t.Fatal(err)
		}
		old, err := waitWorkloadSupervisor(ctx, guest)
		if err != nil {
			t.Fatal(err)
		}
		if err = guest.QuiesceWorkspace(ctx); err != nil {
			t.Fatal(err)
		}
		if err = guest.ImportPrivateWorkspace(ctx, archive); err != nil {
			t.Fatal(err)
		}
		readyCtx, done := context.WithTimeout(ctx, 30*time.Second)
		restarted := false
		for readyCtx.Err() == nil {
			s, err := guest.Status(readyCtx)
			if err == nil && !s.Runtimed.BootedAt.Equal(old.Runtimed.BootedAt) {
				restarted = true
				break
			}
			select {
			case <-readyCtx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		done()
		if !restarted {
			t.Fatal("synthetic import did not restart supervisor")
		}
		channelCtx, close := context.WithCancel(ctx)
		channels = append(channels, close)
		conn, err := guest.OpenEgressChannel(channelCtx)
		if err != nil {
			t.Fatal(err)
		}
		channelResult := make(chan error, 1)
		go func() {
			channelResult <- egress.RunHost(channelCtx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: vm.SandboxID, Generation: vm.RuntimeID}, Policy: workloadEgressPolicy()})
		}()
		if err = guest.ResumeWorkspace(ctx); err != nil {
			t.Fatal(err)
		}
		readyCtx, done = context.WithTimeout(ctx, 20*time.Second)
		ready := false
		var lastPreviewError, lastStatusError string
		var lastStatus *guestapi.Status
		for readyCtx.Err() == nil {
			s, _, err := workloadResponse(readyCtx, hc, *cfg, vm, "GET", "/health")
			status, statusErr := guest.Status(readyCtx)
			lastStatus = status
			if err != nil {
				lastPreviewError = err.Error()
			}
			if statusErr != nil {
				lastStatusError = statusErr.Error()
			}
			if err == nil && statusErr == nil && status.Preview.Status == guestapi.PreviewReady && status.Preview.Pid > 0 && !s.Started && s.Nonce != "" {
				ready = true
				break
			}
			select {
			case <-readyCtx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		done()
		if !ready {
			diagnostic, stop := context.WithTimeout(context.Background(), 10*time.Second)
			status, statusErr := guest.Status(diagnostic)
			logs, logErr := guest.ProcessLogs(diagnostic, "web", 20)
			stop()
			channelState := "still_attached"
			select {
			case channelErr := <-channelResult:
				if channelErr != nil {
					channelState = channelErr.Error()
				} else {
					channelState = "ended_without_error"
				}
			default:
			}
			failure := map[string]any{"guest_index": i, "runtime_id": vm.RuntimeID, "last_status": lastStatus, "last_preview_error": lastPreviewError, "last_status_error": lastStatusError, "status_after_deadline": status, "web_logs": logs, "reverse_channel": channelState}
			if statusErr != nil {
				failure["status_after_deadline_error"] = statusErr.Error()
			}
			if logErr != nil {
				failure["web_logs_error"] = logErr.Error()
			}
			save("readiness-failure-private.json", failure)
			t.Fatal("synthetic app failed readiness; private status/channel/process evidence saved")
		}
		guests = append(guests, guest)
	}
	type result struct {
		Index    int
		Sample   workloadSample
		HTTPMS   float64
		StatusMS float64
		Err      error  `json:"-"`
		Error    string `json:"error,omitempty"`
	}
	batch := func(batchCtx context.Context, start bool) []result {
		results := make([]result, cfg.MaxActive)
		var wg sync.WaitGroup
		for i := range owned {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				method, path := "GET", "/health"
				if start {
					method, path = "POST", "/start"
				}
				s, d, err := workloadResponse(batchCtx, hc, *cfg, owned[i], method, path)
				r := result{Index: i, Sample: s, HTTPMS: float64(d.Microseconds()) / 1000, Err: err}
				if err == nil {
					at := time.Now()
					status, se := guests[i].Status(batchCtx)
					r.StatusMS = float64(time.Since(at).Microseconds()) / 1000
					if se != nil || status.Preview.Status != guestapi.PreviewReady || status.Preview.Pid <= 0 || status.ActiveTask != nil {
						r.Err = errors.New("supervisor/app readiness mismatch")
					}
				}
				if r.Err != nil {
					r.Error = r.Err.Error()
				}
				results[i] = r
			}(i)
		}
		wg.Wait()
		return results
	}

	monitor.phase("preparation")
	prepareStarted := time.Now()
	report["preparation_started_at"] = prepareStarted.UTC()
	prepareCtx, prepareCancel := context.WithTimeout(ctx, 20*time.Second)
	defer prepareCancel()
	initial := batch(prepareCtx, true)
	preparation := [][]result{initial}
	prior := make([]workloadSample, cfg.MaxActive)
	for {
		report["preparation"] = preparation
		save("result.json", report) // retain every response/error even on failure
		resident := true
		latest := preparation[len(preparation)-1]
		for i, r := range latest {
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			if !r.Sample.Started || r.Sample.Bytes != workloadBytes || r.Sample.Nonce == "" || r.Sample.PID <= 0 {
				t.Fatal("synthetic start was not acknowledged")
			}
			if !r.Sample.Active {
				resident = false
				continue
			}
			if e = validateWorkloadSample(workloadSample{}, r.Sample); e != nil {
				t.Fatal(e)
			}
			prior[i] = r.Sample
		}
		if resident {
			break
		}
		select {
		case <-prepareCtx.Done():
			t.Fatal("full resident memory preparation exceeded20s")
		case <-time.After(250 * time.Millisecond):
		}
		preparation = append(preparation, batch(prepareCtx, false))
	}
	prepareCancel()
	report["preparation_seconds"] = time.Since(prepareStarted).Seconds()
	monitor.phase("steady")
	started := time.Now()
	report["steady_work_started_at"] = started.UTC()
	refusalAt := time.Now()
	_, e = create(cfg.MaxActive)
	report["capacity_refusal_ms"] = float64(time.Since(refusalAt).Microseconds()) / 1000
	if !errors.Is(e, cube.ErrCapacityUnavailable) {
		t.Fatal("over-budget allocation not refused safely", e)
	}
	if time.Since(refusalAt) > 5*time.Second {
		t.Fatal("capacity refusal not responsive")
	}
	rounds := [][]result{preparation[len(preparation)-1]}
	hostSamples := []workloadHostSample{}
	for time.Since(started) < 45*time.Second {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
		round := batch(ctx, false)
		report["last_observed_round"] = round
		save("result.json", report)
		for i, r := range round {
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			if e = validateWorkloadSample(prior[i], r.Sample); e != nil {
				t.Fatal(e)
			}
			prior[i] = r.Sample
		}
		rounds = append(rounds, round)
		now, err := workloadMetrics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		hostSamples = append(hostSamples, now)
		if err = workloadMemorySafe(before, now, false); err != nil {
			t.Fatal(err)
		}
		report["rounds"] = rounds
		report["host_samples"] = hostSamples
		save("result.json", report)
	}
	report["all_apps_memory_and_cpu_verified"] = true
	report["over_budget_refused"] = true
	report["measured_load_seconds"] = time.Since(started).Seconds()
	save("result.json", report)
}

func TestWorkloadArchiveAndMemoryGuards(t *testing.T) {
	archive, e := workloadArchive()
	if e != nil {
		t.Fatal(e)
	}
	if e = guestapi.ValidatePrivateWorkspaceArchive(archive); e != nil {
		t.Fatal(e)
	}
	z, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if e != nil || len(z.File) != 2 {
		t.Fatal("synthetic archive must contain exactly manifest and bounded script")
	}
	raw := []byte("host\nMemAvailable: 16777216 kB\nSwapFree: 123 kB\noom_kill 0\n")
	m, e := parseWorkloadMemory(raw)
	if e != nil || m.AvailableKiB != 16<<20 {
		t.Fatal(m, e)
	}
	before := workloadHostSample{Outer: m, Worker: m}
	if e = workloadMemorySafe(before, before, true); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*workloadHostSample){func(s *workloadHostSample) { s.Worker.OOMKills++ }, func(s *workloadHostSample) { s.Outer.OOMKills++ }, func(s *workloadHostSample) { s.Worker.AvailableKiB = 1 }, func(s *workloadHostSample) { s.Outer.AvailableKiB = 1 }} {
		after := before
		change(&after)
		if workloadMemorySafe(before, after, false) == nil {
			t.Fatal("unsafe memory evidence accepted")
		}
	}
	for _, bad := range []string{"host\nMemAvailable: 10 kB\n", "host\nMemAvailable: 1 MB\nSwapFree: 0 kB\noom_kill 0\n", string(raw) + "oom_kill 0\n"} {
		if _, e = parseWorkloadMemory([]byte(bad)); e == nil {
			t.Fatal("incomplete/ambiguous metrics accepted")
		}
	}
}
func TestWorkloadRequiresActualResidentPagesAndProgress(t *testing.T) {
	good := workloadSample{Nonce: strings.Repeat("a", 32), PID: 42, Started: true, Active: true, Bytes: workloadBytes, Pages: workloadBytes / 4096 * 90, RSS: workloadBytes, Cycles: 2}
	good.CPU.User = 100
	if e := validateWorkloadSample(workloadSample{}, good); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*workloadSample){func(s *workloadSample) { s.Pages-- }, func(s *workloadSample) { s.RSS-- }, func(s *workloadSample) { s.Bytes-- }, func(s *workloadSample) { s.Active = false }} {
		bad := good
		change(&bad)
		if validateWorkloadSample(workloadSample{}, bad) == nil {
			t.Fatal("fake memory work accepted")
		}
	}
	if validateWorkloadSample(good, good) == nil {
		t.Fatal("no CPU progress accepted")
	}
	next := good
	next.Cycles++
	next.CPU.User++
	if e := validateWorkloadSample(good, next); e != nil {
		t.Fatal(e)
	}
	next.Nonce = strings.Repeat("b", 32)
	if validateWorkloadSample(good, next) == nil {
		t.Fatal("process restart accepted")
	}
}

func TestWorkloadWaitsForAuthenticatedSupervisor(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("readiness missing scoped authorization")
			w.WriteHeader(401)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(502)
			return
		}
		_ = json.NewEncoder(w).Encode(guestapi.Status{Runtimed: guestapi.RuntimedInfo{BootedAt: time.Now().UTC()}})
	}))
	defer server.Close()
	guest, err := guestapi.NewRemoteClient(guestapi.RemoteConfig{BaseURL: server.URL, Host: "3031-fixture.cube.app", Token: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = waitWorkloadSupervisor(ctx, guest); err != nil || calls.Load() != 2 {
		t.Fatalf("transient readiness was not retried: %v calls=%d", err, calls.Load())
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err = waitWorkloadSupervisor(canceled, guest); err == nil || calls.Load() != 2 {
		t.Fatal("canceled readiness made another request")
	}
}

func TestWorkloadEgressPolicyIsValidAndDeniesAllFamilies(t *testing.T) {
	policy := workloadEgressPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"8.8.8.8", "10.0.0.1", "2606:4700:4700::1111"} {
		if _, err := policy.Destination(context.Background(), address, 443); !errors.Is(err, egress.ErrDenied) {
			t.Fatalf("fixture destination unexpectedly permitted: %s %v", address, err)
		}
	}
}

func TestWorkloadAllowsOnlyReviewedSlotCounts(t *testing.T) {
	for _, slots := range []int{4, 6, 8, 12} {
		if !validWorkloadSlots(slots) {
			t.Fatal("reviewed slot count refused")
		}
	}
	for _, slots := range []int{-1, 0, 1, 3, 5, 7, 9, 11, 13, 24} {
		if validWorkloadSlots(slots) {
			t.Fatal("unreviewed workload scale accepted")
		}
	}
}

// Explicit fixture profiles; omission is not silently treated as a resource choice.
func workloadProfile(name string) (cube.AdmissionResources, error) {
	switch name {
	case "cpu1-mem1024":
		return cube.AdmissionResources{CPUCount: 1, MemoryMB: 1024}, nil
	case "cpu1-mem2048":
		return cube.AdmissionResources{CPUCount: 1, MemoryMB: 2048}, nil
	case "cpu2-mem2048":
		return cube.AdmissionResources{CPUCount: 2, MemoryMB: 2048}, nil
	default:
		return cube.AdmissionResources{}, errors.New("explicit reviewed fixture profile required")
	}
}
func TestWorkloadProfilesAreExplicit(t *testing.T) {
	for _, name := range []string{"cpu1-mem1024", "cpu1-mem2048", "cpu2-mem2048"} {
		if _, err := workloadProfile(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"", "cpu2-mem1024", "cpu4-mem2048", "cpu1-mem4096"} {
		if _, err := workloadProfile(name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
