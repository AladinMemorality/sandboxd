// Owned synthetic crash coordinator. It never performs a power operation.
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

const template = "tpl-ce1ee426e686460bbc8c3bfc"

type marker struct {
	Fixture string `json:"fixture"`
	Phase   string `json:"phase"`
	Nonce   string `json:"nonce"`
}
type escrow struct {
	Guest      *cube.Sandbox `json:"guest"`
	Supervisor string        `json:"supervisor"`
	Capability string        `json:"capability"`
	Fixture    string        `json:"fixture"`
	BootID     string        `json:"worker_boot_id"`
	WorkerUUID string        `json:"worker_uuid"`
	DataUUID   string        `json:"data_uuid"`
	Baseline   marker        `json:"baseline"`
	Latest     marker        `json:"latest"`
}
type coordinator struct {
	ctx    context.Context
	stage  string
	cube   *cube.Client
	guest  *rt.Client
	saved  escrow
	cancel context.CancelFunc
	report map[string]any
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func nonce(n int) string {
	b := make([]byte, n)
	_, e := rand.Read(b)
	must(e)
	return hex.EncodeToString(b)
}
func durable(path string, data []byte, exclusive bool) error {
	if exclusive {
		if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			return errors.New("evidence path already exists")
		}
	}
	f, e := os.OpenFile(path+".pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path + ".pending")
		}
	}()
	if _, e = f.Write(data); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(path+".pending", path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	e = d.Sync()
	ok = e == nil
	return e
}
func save(path string, v any, exclusive bool) {
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(durable(path, append(b, '\n'), exclusive))
}
func privateJSON(path string, out any) error {
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 1<<20 {
		return errors.New("private bounded root evidence required")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func worker(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-p", "20222", "root@127.0.0.1", command)
	b, e := cmd.Output()
	if e != nil || len(b) > 65536 {
		return "", errors.New("bounded worker identity/inventory read failed")
	}
	return strings.TrimSpace(string(b)), nil
}
func bootID(ctx context.Context) string {
	b, e := worker(ctx, "cat /proc/sys/kernel/random/boot_id")
	must(e)
	if !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(b) {
		panic("invalid worker boot identity")
	}
	return b
}
func (c *coordinator) record(stage string) {
	c.report["stage"] = stage
	c.report["updated_at"] = time.Now().UTC()
	save(filepath.Join(c.stage, "report.json"), c.report, false)
}
func (c *coordinator) detach() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}
func (c *coordinator) attach() {
	c.detach()
	for i := 0; i < 40; i++ {
		conn, e := c.guest.OpenEgressChannel(c.ctx)
		if e == nil {
			ctx, cancel := context.WithCancel(c.ctx)
			c.cancel = cancel
			go func() {
				_ = egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: c.saved.Guest.SandboxID, Generation: c.saved.Fixture}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}, DialContext: func(context.Context, string, string) (net.Conn, error) {
					return nil, errors.New("synthetic fixture denies all outbound dial")
				}})
			}()
			return
		}
		select {
		case <-c.ctx.Done():
			panic("reverse channel deadline")
		case <-time.After(500 * time.Millisecond):
		}
	}
	panic("authenticated reverse channel unavailable")
}
func (c *coordinator) remote() {
	var e error
	c.guest, e = rt.NewRemoteClient(rt.RemoteConfig{BaseURL: "http://127.0.0.1:20080", Host: "3031-" + c.saved.Guest.SandboxID + ".cube.app", Token: c.saved.Supervisor, TrafficAccessToken: c.saved.Guest.TrafficAccessToken})
	must(e)
}
func (c *coordinator) ownership(ctx context.Context) *cube.Sandbox {
	got, e := c.cube.Get(ctx, c.saved.Guest.SandboxID)
	must(e)
	if got.SandboxID != c.saved.Guest.SandboxID || got.TemplateID != template || got.CPUCount != 2 || got.MemoryMB != 2048 || got.Metadata["operator-crash-fixture"] != c.saved.Fixture {
		panic("immutable guest ownership/resource mismatch")
	}
	return got
}
func (c *coordinator) request(port int, path string, body any) ([]byte, error) {
	var data []byte
	var e error
	method := "GET"
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return nil, e
		}
		method = "POST"
	}
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	r, e := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:20080"+path, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	r.Host = fmt.Sprintf("%d-%s.cube.app", port, c.saved.Guest.SandboxID)
	r.Header.Set("Cube-Traffic-Access-Token", c.saved.Guest.TrafficAccessToken)
	if port == 3006 {
		r.Header.Set("Authorization", "Bearer "+c.saved.Capability)
	}
	r.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(r)
	if e != nil {
		return nil, errors.New("synthetic app request unavailable")
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("invalid bounded app response")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("synthetic app HTTP%d", res.StatusCode)
	}
	return b, nil
}
func (c *coordinator) ready() {
	for i := 0; i < 40; i++ {
		b, e := c.request(3000, "/health", nil)
		if e == nil && bytes.Contains(b, []byte(`"status":"ok"`)) {
			return
		}
		select {
		case <-c.ctx.Done():
			panic("app readiness deadline")
		case <-time.After(500 * time.Millisecond):
		}
	}
	panic("actual PostgreSQL application health unavailable")
}
func validateEvidence(data []byte, want marker) error {
	var got struct {
		Matches  bool   `json:"matches"`
		App      marker `json:"app"`
		Home     marker `json:"home"`
		Database marker `json:"database"`
	}
	if json.Unmarshal(data, &got) != nil || !got.Matches || got.App != want || got.Home != want || got.Database != want {
		return errors.New("latest committed app/home/SQL marker mismatch")
	}
	return nil
}
func (c *coordinator) probe(route string, want marker) {
	b, e := c.request(3006, route, want)
	must(e)
	must(validateEvidence(b, want))
}
func (c *coordinator) install() {
	b, e := c.guest.ExportSource(c.ctx)
	must(e)
	original, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	must(e)
	manifest, e := c.guest.ReadFile(c.ctx, "sandbox.yaml")
	must(e)
	if !bytes.Contains(manifest, []byte("name: postgres")) || strings.Count(string(manifest), "\nbuild:") != 1 {
		panic("reviewed PostgreSQL manifest required")
	}
	yaml := strings.Replace(string(manifest), "\nbuild:", "\n  - name: crash-probe\n    command: \"chmod 600 crash-fixture-token && node probe.mjs\"\n    restart_after_task: false\nbuild:", 1)
	probe, e := os.ReadFile(filepath.Join(c.stage, "probe.mjs"))
	must(e)
	settings, _ := json.Marshal(map[string]string{"purpose": "DISPOSABLE_POSTGRES_CRASH_ONLY", "fixture": c.saved.Fixture})
	replacements := map[string][]byte{"sandbox.yaml": []byte(yaml), "probe.mjs": probe, "config/crash-fixture.json": settings, "crash-fixture-token": []byte(c.saved.Capability)}
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	for _, f := range original.File {
		if _, ok := replacements[f.Name]; ok {
			continue
		}
		r, e := f.Open()
		must(e)
		out, e := w.Create(f.Name)
		must(e)
		_, e = io.Copy(out, r)
		r.Close()
		must(e)
	}
	for name, value := range replacements {
		if !rt.PublishedSourcePath(name) {
			panic("synthetic source path would be filtered")
		}
		out, e := w.Create(name)
		must(e)
		_, e = out.Write(value)
		must(e)
	}
	must(w.Close())
	before, e := c.guest.Status(c.ctx)
	must(e)
	must(c.guest.ImportSource(c.ctx, output.Bytes()))
	restarted := false
	for i := 0; i < 40; i++ {
		status, e := c.guest.Status(c.ctx)
		if e == nil && !status.Runtimed.BootedAt.Equal(before.Runtimed.BootedAt) {
			restarted = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !restarted {
		panic("probe import did not reexec")
	}
	c.attach()
	must(c.guest.ResumeWorkspace(c.ctx))
	c.ready()
	probeReady := false
	for i := 0; i < 30; i++ {
		b, e := c.request(3006, "/status", nil)
		if e == nil && bytes.Contains(b, []byte(c.saved.Fixture)) {
			probeReady = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !probeReady {
		panic("synthetic probe did not become ready")
	}
	// A verify before initial commit must fail: it cannot silently initialize data.
	if _, e = c.request(3006, "/verify", c.saved.Baseline); e == nil {
		panic("probe contained preexisting synthetic markers")
	}
}
func homeManifest() rt.HomeManifest {
	return rt.HomeManifest{Version: 2, Entries: []rt.HomeManifestEntry{{Path: "workspace/app", Disposition: "separate"}, {Path: ".runtimed", Disposition: "separate"}, {Path: ".bashrc", Disposition: "preserve"}, {Path: ".bash_logout", Disposition: "preserve"}, {Path: ".profile", Disposition: "preserve"}, {Path: ".cache", Disposition: "preserve"}, {Path: ".baarcha-postgres", Disposition: "preserve"}, {Path: ".cube-crash-fixture", Disposition: "preserve"}}}
}
func (c *coordinator) export(name string, fn func(io.Writer) error) map[string]any {
	f, e := os.OpenFile(filepath.Join(c.stage, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	must(e)
	defer f.Close()
	must(fn(f))
	must(f.Sync())
	info, e := f.Stat()
	must(e)
	_, e = f.Seek(0, io.SeekStart)
	must(e)
	hash := sha256.New()
	_, e = io.Copy(hash, f)
	must(e)
	result := map[string]any{"file": name, "bytes": info.Size(), "sha256": hex.EncodeToString(hash.Sum(nil))}
	_, e = f.Seek(0, io.SeekStart)
	must(e)
	if name == "app-before.zip" {
		d, e := rt.PrivateWorkspaceFileDigest(f, info.Size())
		must(e)
		result["tree_digest"] = d
	} else {
		d, e := rt.PrivateHomeDigest(homeManifest(), f, info.Size())
		must(e)
		result["tree_digest"] = d
	}
	d, e := os.Open(c.stage)
	must(e)
	must(d.Sync())
	must(d.Close())
	return result
}
func (c *coordinator) cleanup() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c.detach()
	_, e := c.cube.Get(ctx, c.saved.Guest.SandboxID)
	var ae *cube.APIError
	if errors.As(e, &ae) && ae.StatusCode == 404 {
		return true
	}
	c.ownership(ctx)
	must(c.cube.Delete(ctx, c.saved.Guest.SandboxID))
	_, e = c.cube.Get(ctx, c.saved.Guest.SandboxID)
	return errors.As(e, &ae) && ae.StatusCode == 404
}
func emptyInventory(value string) bool {
	matches := regexp.MustCompile(`(?m)^\s*SANDBOX_COUNT\s+(\d+)\s*$`).FindAllStringSubmatch(value, -1)
	return len(matches) == 1 && matches[0][1] == "0"
}

func (c *coordinator) prepare() {
	hostname, e := worker(c.ctx, "hostname")
	must(e)
	if hostname != "baarcha-cube-worker-01" {
		panic("fresh worker required")
	}
	inventory, e := worker(c.ctx, "cubemastercli -a 127.0.0.1 list --all --wide")
	must(e)
	if !emptyInventory(inventory) {
		panic("worker inventory is not empty")
	}
	var handoff struct {
		Purpose    string `json:"purpose"`
		NoCustomer bool   `json:"no_customer_guests"`
		Cleanup    bool   `json:"previous_family_cleanup_verified"`
		WorkerUUID string `json:"worker_uuid"`
		Expires    int64  `json:"expires_at"`
	}
	must(privateJSON(filepath.Join(c.stage, "handoff.json"), &handoff))
	now := time.Now().Unix()
	if handoff.Purpose != "DISPOSABLE_CUBE_CRASH_HANDOFF" || !handoff.NoCustomer || !handoff.Cleanup || handoff.Expires <= now || handoff.Expires > now+1800 {
		panic("fresh crash handoff required")
	}
	var proof map[string]any
	must(privateJSON(filepath.Join(c.stage, "postgres-lifecycle-report.json"), &proof))
	for _, key := range []string{"all_vms_deleted", "database_write_read", "fresh_remix_database_empty", "database_survives_pause_resume", "database_survives_supervisor_reexec", "database_survives_owner_source_restore", "postgres_home_v2_roundtrip"} {
		if proof[key] != true {
			panic("full PostgreSQL functional acceptance required first")
		}
	}
	workerUUID, e := worker(c.ctx, "cat /sys/class/dmi/id/product_uuid")
	must(e)
	dataUUID, e := worker(c.ctx, "findmnt -n -o UUID /data")
	must(e)
	if workerUUID == "" || !strings.EqualFold(workerUUID, handoff.WorkerUUID) || dataUUID == "" {
		panic("operator worker UUID or data identity mismatch")
	}
	c.saved = escrow{Fixture: nonce(16), Supervisor: nonce(32), Capability: nonce(32), BootID: bootID(c.ctx)}
	c.saved.WorkerUUID = workerUUID
	c.saved.DataUUID = dataUUID
	c.report["worker_uuid"] = workerUUID
	c.report["data_uuid"] = dataUUID
	c.saved.Baseline = marker{c.saved.Fixture, "baseline", nonce(16)}
	c.saved.Latest = marker{c.saved.Fixture, "latest", nonce(16)}
	save(filepath.Join(c.stage, "intent.private.json"), c.saved, true)
	c.record("create-intent-durable")
	var e2 error
	c.saved.Guest, e2 = c.cube.Create(c.ctx, cube.CreateRequest{TemplateID: template, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": c.saved.Supervisor}, Metadata: map[string]string{"operator-crash-fixture": c.saved.Fixture}, TimeoutSeconds: 1800, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}})
	must(e2)
	save(filepath.Join(c.stage, "escrow.private.json"), c.saved, true)
	c.report["sandbox_id"] = c.saved.Guest.SandboxID
	c.report["fixture"] = c.saved.Fixture
	c.record("owned-id-escrowed")
	c.ownership(c.ctx)
	c.remote()
	c.attach()
	c.ready()
	c.install()
	c.probe("/commit", c.saved.Baseline)
	must(c.guest.QuiesceWorkspace(c.ctx))
	c.report["app_export"] = c.export("app-before.zip", func(w io.Writer) error { return c.guest.ExportPrivateWorkspaceFile(c.ctx, w) })
	c.report["home_export"] = c.export("home-before.zip", func(w io.Writer) error { return c.guest.ExportPrivateHome(c.ctx, homeManifest(), w) })
	save(filepath.Join(c.stage, "home-manifest.json"), homeManifest(), true)
	c.report["baseline"] = c.saved.Baseline
	c.record("independent-baseline-exports-durable")
	must(c.guest.ResumeWorkspace(c.ctx))
	c.ready()
	c.probe("/commit", c.saved.Latest)
	c.probe("/verify", c.saved.Latest)
	c.report["latest"] = c.saved.Latest
	c.report["latest_acknowledged_at"] = time.Now().UTC()
	c.report["worker_boot_before"] = c.saved.BootID
	c.report["checkpoint_ready"] = true
	c.record("WAITING_FOR_OPERATOR_POWER_LOSS")
	fmt.Println("CHECKPOINT_READY: owned synthetic latest app/home/SQL markers and older independent exports are durable; no power operation performed")
}
func (c *coordinator) verify() {
	var confirmation struct {
		Purpose      string `json:"purpose"`
		Fixture      string `json:"fixture"`
		PreviousBoot string `json:"previous_boot_id"`
		Method       string `json:"method"`
	}
	must(privateJSON(filepath.Join(c.stage, "power-loss-complete.json"), &confirmation))
	if confirmation.Purpose != "DISPOSABLE_CUBE_CRASH_COMPLETE" || confirmation.Fixture != c.saved.Fixture || confirmation.PreviousBoot != c.saved.BootID || confirmation.Method != "operator-reviewed-power-loss" {
		panic("exact operator power-loss confirmation required")
	}
	after := bootID(c.ctx)
	if after == c.saved.BootID {
		panic("worker did not reboot")
	}
	workerUUID, e := worker(c.ctx, "cat /sys/class/dmi/id/product_uuid")
	must(e)
	dataUUID, e := worker(c.ctx, "findmnt -n -o UUID /data")
	must(e)
	if workerUUID != c.saved.WorkerUUID || dataUUID != c.saved.DataUUID {
		panic("post-crash worker or data disk identity changed")
	}
	c.report["worker_boot_after"] = after
	detail := c.ownership(c.ctx)
	if detail.State == "paused" {
		_, e := c.cube.Connect(c.ctx, detail.SandboxID, cube.ConnectRequest{TimeoutSeconds: 600})
		must(e)
	} else if detail.State != "running" {
		panic("owned guest state does not support ordinary connect")
	}
	c.remote()
	c.attach()
	c.ready()
	c.probe("/verify", c.saved.Latest)
	c.report["post_power_loss_latest_app_home_sql_preserved"] = true
	c.record("verified-latest-data-without-restore")
}

// Hold the same worker-local fixture lock as the API/preset coordinators.
// A worker reboot drops this process; the explicit crash handoff then owns
// exclusivity until cleanup. No command here changes power or networking.
func holdWorkerLease(ctx context.Context) func() {
	leaseCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(leaseCtx, "ssh", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-p", "20222", "root@127.0.0.1", `flock -n /run/lock/cube-operator-acceptance.lock sh -c 'printf "LOCKED\n"; cat >/dev/null'`)
	input, e := cmd.StdinPipe()
	must(e)
	output, e := cmd.StdoutPipe()
	must(e)
	must(cmd.Start())
	ready := make(chan bool, 1)
	go func() { line, e := bufio.NewReader(output).ReadString('\n'); ready <- e == nil && line == "LOCKED\n" }()
	select {
	case ok := <-ready:
		if !ok {
			cancel()
			input.Close()
			cmd.Wait()
			panic("worker fixture lock unavailable")
		}
	case <-time.After(10 * time.Second):
		cancel()
		input.Close()
		cmd.Wait()
		panic("worker lock readiness deadline")
	}
	return func() { input.Close(); cancel(); _ = cmd.Wait() }
}

func (c *coordinator) recordFailure() {
	if p := recover(); p != nil {
		c.report["failure"] = fmt.Sprint(p)
		if c.saved.Guest != nil {
			if c.report["checkpoint_ready"] == true {
				// Preserve post-crash forensic state; only the explicit cleanup command
				// may delete a failed crash target after the coordinator's review.
				c.report["cleanup_verified_http404"] = false
				c.report["owned_guest_retained_for_review"] = true
			} else {
				cleaned := c.cleanup()
				c.report["cleanup_verified_http404"] = cleaned
				c.report["owned_guest_retained_for_review"] = !cleaned
			}
		}
		c.record("failed")
		panic(p)
	}
}

func run() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("synthetic crash fixture stopped: %v", p)
		}
	}()
	if len(os.Args) != 3 || (os.Args[1] != "run" && os.Args[1] != "verify" && os.Args[1] != "cleanup") {
		return errors.New("usage: crash-coordinator run|verify|cleanup PRIVATE_STAGE")
	}
	stage := filepath.Clean(os.Args[2])
	if !strings.HasPrefix(stage, "/opt/baarcha-bench/cube-crash-") || os.Geteuid() != 0 {
		return errors.New("private outer-host operator stage required")
	}
	info, e := os.Lstat(stage)
	must(e)
	st := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || st.Uid != 0 {
		panic("private root stage required")
	}
	lock, e := os.OpenFile("/run/lock/cube-operator-acceptance.lock", os.O_RDWR|os.O_CREATE, 0600)
	must(e)
	defer lock.Close()
	must(syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	releaseWorkerLease := holdWorkerLease(ctx)
	defer releaseWorkerLease()
	env, e := os.ReadFile("/opt/baarcha-cube/worker-01/staging/cube-install.env")
	must(e)
	key := ""
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "CUBE_API_KEY=") {
			key = strings.Trim(strings.TrimPrefix(line, "CUBE_API_KEY="), "\"'")
		}
	}
	if key == "" {
		panic("private Cube API key unavailable")
	}
	api, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: key})
	must(e)
	c := &coordinator{ctx: ctx, stage: stage, cube: api, report: map[string]any{"production_accepted": false, "power_operation_performed_by_fixture": false, "template": template}}
	defer c.detach()
	if os.Args[1] == "run" {
		if _, e = os.Lstat(filepath.Join(stage, "report.json")); !errors.Is(e, os.ErrNotExist) {
			panic("prior evidence exists; refuse overwrite")
		}
		defer c.recordFailure()
		c.prepare()
		deadline := time.Now().Add(15 * time.Minute)
		for {
			if _, e = os.Lstat(filepath.Join(stage, "power-loss-complete.json")); e == nil {
				break
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				panic("operator power-loss checkpoint expired")
			}
			time.Sleep(time.Second)
		}
	} else {
		must(privateJSON(filepath.Join(stage, "escrow.private.json"), &c.saved))
		must(privateJSON(filepath.Join(stage, "report.json"), &c.report))
		defer c.recordFailure()
	}
	if os.Args[1] != "cleanup" {
		if os.Args[1] == "run" {
			// The pre-crash worker-local flock died with that kernel. Reacquire
			// exclusivity on the restarted worker before any verification request.
			releaseAfterCrash := holdWorkerLease(ctx)
			defer releaseAfterCrash()
		}
		c.verify()
	}
	c.report["cleanup_verified_http404"] = c.cleanup()
	c.record("finished")
	if c.report["cleanup_verified_http404"] != true {
		return errors.New("owned cleanup not proven; retain escrow for review")
	}
	fmt.Println("PASS: owned fixture completed and independent Cube GET404 verified")
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
