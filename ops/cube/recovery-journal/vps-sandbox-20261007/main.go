// Exact-incident, offline operator. Artifacts remain private on the VPS.
// Never deletes the original runtime, retries an ambiguous create, or rewrites app identity.
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/recovery"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

const stage = "/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007"
const sandbox = "01M3HH7PZJRPKHZ9GE3NRC37F7"
const appID = "01M3HH7PV53KPQN0CNKS78Z1QG"
const oldRuntime = "f51584459b354470b04c966ae8429293"
const template = "tpl-98b45d63cfcc48c5b6ba9104"
const journalID = "vps-sandbox-20261007"

type config struct {
	Database, Migrations, KeyFile, MasterURL, ProxyURL string
	Provider                                           cube.Config
	Policy                                             cube.AdmissionConfig
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func readJSON(path string, v any) { b, e := os.ReadFile(path); must(e); must(json.Unmarshal(b, v)) }
func hash(path string) string {
	f, e := os.Open(path)
	must(e)
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	must(e)
	return fmt.Sprintf("%x", h.Sum(nil))
}
func save(name string, v any) {
	b, e := json.Marshal(v)
	must(e)
	p := filepath.Join(stage, name)
	f, e := os.OpenFile(p+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	must(e)
	_, e = f.Write(b)
	must(e)
	must(f.Sync())
	must(f.Close())
	must(os.Rename(p+".tmp", p))
}
func mark(step string) {
	save("operator-progress.json", map[string]any{"step": step, "at": time.Now().UTC()})
	fmt.Println(step)
}
func file(path string) (*os.File, int64) {
	f, e := os.Open(path)
	must(e)
	i, e := f.Stat()
	must(e)
	if !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 {
		panic("private regular artifact required")
	}
	return f, i.Size()
}
func lock(path string) *os.File {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	must(e)
	must(syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	return f
}
func worker(ctx context.Context, cmd string) []byte {
	v, e := exec.CommandContext(ctx, "nsenter", "-t", "1", "-n", "ssh", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-p", "20222", "root@127.0.0.1", cmd).Output()
	must(e)
	return v
}
func workerLease(ctx context.Context) func() {
	c := exec.CommandContext(ctx, "nsenter", "-t", "1", "-n", "ssh", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-p", "20222", "root@127.0.0.1", `flock -n /run/lock/cube-operator-acceptance.lock sh -c 'printf "LOCKED\n"; cat >/dev/null'`)
	in, e := c.StdinPipe()
	must(e)
	out, e := c.StdoutPipe()
	must(e)
	must(c.Start())
	line, e := bufio.NewReader(out).ReadString('\n')
	must(e)
	if line != "LOCKED\n" {
		panic("worker lock missing")
	}
	return func() {
		in.Close()
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
		_ = c.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
		_ = c.Process.Kill()
		<-done
	}
}
func artifacts() (rt.HomeManifest, *os.File, int64, *os.File, int64, string, string) {
	var m rt.HomeManifest
	readJSON(stage+"/converted/home-manifest.json", &m)
	a, an := file(stage + "/converted/app.zip")
	h, hn := file(stage + "/converted/home.zip")
	ad, e := rt.PrivateWorkspaceFileDigest(a, an)
	must(e)
	hd, e := rt.PrivateHomeDigest(m, h, hn)
	must(e)
	must(rt.ValidatePrivateWorkspaceFileInterpreters(a, an))
	var conv map[string]any
	readJSON(stage+"/converted/conversion.json", &conv)
	if hash(a.Name()) != conv["app_sha256"] || hash(h.Name()) != conv["home_sha256"] || hash(stage+"/home.tar") != conv["archive_sha256"] {
		panic("artifact hash mismatch")
	}
	return m, a, an, h, hn, ad, hd
}
func loadAppConfig(ctx context.Context, cfg config, key *secrets.Cipher) rt.AppConfigRequest {
	db, e := store.Open(ctx, "file:"+cfg.Database+"?_journal=WAL&_busy_timeout=5000&_fk=1", cfg.Migrations)
	must(e)
	b, e := db.GetRuntimeBinding(ctx, sandbox)
	must(e)
	if b.RuntimeID != oldRuntime || b.ConfigRevision != 0 {
		panic("original binding changed")
	}
	entries, e := appenv.For(ctx, db, key, appID)
	must(e)
	must(db.Close())
	env := map[string]string{}
	for _, v := range entries {
		k, v, _ := strings.Cut(v, "=")
		env[k] = v
	}
	req := rt.AppConfigRequest{Env: env, Revision: sandbox + ":0"}
	must(rt.ValidateAppConfig(req))
	return req
}
func attach(ctx context.Context, g *rt.Client, id string) context.CancelFunc {
	for i := 0; i < 80; i++ {
		conn, e := g.OpenEgressChannel(ctx)
		if e == nil {
			sub, cancel := context.WithCancel(ctx)
			go func() {
				_ = egress.RunHost(sub, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: id, Generation: journalID}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}}, DialContext: func(context.Context, string, string) (net.Conn, error) {
					return nil, errors.New("offline recovery denies outbound traffic")
				}})
			}()
			return cancel
		}
		time.Sleep(250 * time.Millisecond)
	}
	panic("reverse channel unavailable")
}
func await(ctx context.Context, g *rt.Client, test func(*rt.Status) bool) {
	for i := 0; i < 160; i++ {
		s, e := g.Status(ctx)
		if e == nil && test(s) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	panic("supervisor readiness deadline")
}

const originalTask = "01M3HH7R48XW80033ZNP625BZ3"

func historyFiles(data []byte) map[string]string {
	must(rt.ValidatePrivateTaskHistoryArchive(data))
	ids, e := rt.PrivateTaskHistoryIDs(data)
	must(e)
	if len(ids) != 1 || ids[0] != originalTask {
		panic("owned terminal task identity changed")
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	must(e)
	out := map[string]string{}
	for _, f := range z.File {
		r, e := f.Open()
		must(e)
		h := sha256.New()
		_, e = io.Copy(h, r)
		r.Close()
		must(e)
		out[f.Name] = fmt.Sprintf("%x", h.Sum(nil))
	}
	return out
}
func run() {
	if len(os.Args) != 2 || os.Geteuid() != 0 {
		panic("native root and validate/preflight/run required")
	}
	cmd := os.Args[1]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	var cfg config
	readJSON(stage+"/operator-config.private.json", &cfg)
	must(cfg.Policy.RequireStorageGuard())
	m, a, an, h, hn, ad, hd := artifacts()
	defer a.Close()
	defer h.Close()
	history, e := os.ReadFile(stage + "/converted/history.zip")
	must(e)
	historyExpected := historyFiles(history)
	var report, repair map[string]any
	readJSON(stage+"/export-report.json", &report)
	readJSON(stage+"/repair-receipt.json", &repair)
	if repair["clean_verified"] != true || hash(stage+"/repair-receipt.json") != report["explicit_repair_receipt_sha256"] || repair["source_sha256"] != report["captured_disk_sha256"] || repair["clone_after_sha256"] != report["explicit_repair_clone_sha256"] {
		panic("verified disposable filesystem repair receipt required")
	}

	fmt.Println("canonical archives and owned terminal task history validated")
	if cmd == "validate" {
		save("go-validation.json", map[string]any{"valid": true, "workspace_digest": ad, "home_digest": hd})
		return
	}
	if cmd != "preflight" && cmd != "run" && cmd != "continue" && cmd != "verify-imported" {
		panic("unknown command")
	}
	if cmd == "preflight" {
		cfg.Database = stage + "/preflight.db"
		l, e := maintenance.Acquire(cfg.Database, false)
		must(e)
		must(l.Close())
	}
	key, e := secrets.Load("", cfg.KeyFile)
	must(e)
	// Configuration is read only while all controller writers are fenced below.
	if cmd == "run" || cmd == "continue" || cmd == "verify-imported" {
		for _, p := range []string{"/opt/baarcha/deploy-release.lock", "/opt/sandboxd/deploy-state/deploy.lock", "/run/lock/cube-operator-acceptance.lock", "/opt/baarcha-bench/cube-workload-operator.lock"} {
			f := lock(p)
			defer f.Close()
		}
		release := workerLease(ctx)
		defer release()
		defer func() {
			out, e := exec.Command("python3", stage+"/maintenance.py", "exit").CombinedOutput()
			fmt.Print(string(out))
			if e != nil {
				fmt.Println("controller restore requires operator review")
			}
		}()
		if cmd == "run" {
			out, e := exec.Command("python3", stage+"/maintenance.py", "enter").CombinedOutput()
			fmt.Print(string(out))
			must(e)
		} else {
			out, e := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", "src-sandboxd-1").Output()
			must(e)
			if strings.TrimSpace(string(out)) != "false" {
				panic("controller must remain stopped")
			}
		}
	}
	// Open verifies exclusive daemon maintenance and the full durable worker policy.
	session, e := recovery.OpenPinnedWorker(ctx, cfg.Database, cfg.Migrations, key, cfg.Provider, cfg.Policy, cfg.MasterURL, "vps")
	must(e)
	defer session.Close()
	// Opening a second store is deliberately avoided while Session checks /proc.
	// Load from the controller backup, whose bytes were captured after stop.
	frozen := cfg
	if cmd == "run" || cmd == "continue" || cmd == "verify-imported" {
		frozen.Database = stage + "/controller-before.db"
	}
	request := loadAppConfig(ctx, frozen, key)
	if cmd == "preflight" {
		fmt.Println("production policy and frozen config preflight passed")
		return
	}
	var fence struct {
		OldRuntimeID                                 string
		OldExecutionStopped, ProviderRequestsDrained bool
		Expires                                      int64
	}
	readJSON(stage+"/fence.json", &fence)
	if fence.OldRuntimeID != oldRuntime || !fence.OldExecutionStopped || !fence.ProviderRequestsDrained || (cmd == "run" && fence.Expires < time.Now().Unix()) {
		panic("fresh independently verified source fence required")
	}
	worker(ctx, `python3 -c 'import pathlib,json,hashlib
r=pathlib.Path("/data/cube-recovery/vps-sandbox-20261007");p=json.loads((r/"metadata/plan.json").read_text());source=pathlib.Path(p["current_disk"]["FilePath"]);st=source.stat();assert not source.is_symlink()
for proc in pathlib.Path("/proc").glob("[0-9]*"):
 for fd in (proc/"fd").glob("*"):
  try:s=fd.stat()
  except OSError:continue
  assert (s.st_dev,s.st_ino)!=(st.st_dev,st.st_ino)
expected=json.loads((r/"capture/rescue-input.json").read_text())["artifacts"][0]["sha256"]
with source.open("rb") as f:assert hashlib.file_digest(f,"sha256").hexdigest()==expected
print("source unchanged and no live handles")'`)

	tasks := worker(ctx, "ctr --address /data/cubelet/cubelet.sock --namespace default tasks list")
	if strings.Contains(string(tasks), oldRuntime) {
		panic("original runtime task reappeared")
	}
	paths := map[string]string{"rescue_export_report": stage + "/export-report.json", "filesystem_repair": stage + "/repair-receipt.json", "capture_manifest": stage + "/native/rescue-input.json", "history": stage + "/converted/history.zip", "native_backup": stage + "/native/current.ext4", "controller_backup": stage + "/controller-before.db", "workspace": a.Name(), "home": h.Name(), "merged_home": stage + "/home.tar", "home_manifest": stage + "/converted/home-manifest.json", "source_fence": stage + "/fence.json"}
	hashes := map[string]string{}
	for role, p := range paths {
		hashes[role] = hash(p)
	}
	if hashes["native_backup"] != report["captured_disk_sha256"] {
		panic("filesystem repair source mismatch")
	}
	plan := store.CubeRecoveryPlan{ID: journalID, SandboxID: sandbox, ExpectedRuntimeID: oldRuntime, TargetTemplateID: template, TargetDomain: "cube.app", ExpectedConfigRevision: 0, Artifacts: hashes, ArtifactPaths: paths}

	var j *store.CubeRecoveryJournal
	if cmd == "run" {
		must(session.Begin(ctx, plan))
		mark("journal-held")
		j, e = session.Journal(ctx, journalID)
		must(e)
		if j.TaskCount != 1 || j.AppID != appID {
			panic("unexpected app/task identity")
		}
		must(session.Fence(ctx, store.CubeRecoveryFence{RecoveryID: j.ID, OldRuntimeID: oldRuntime, ArtifactsSHA256: j.ArtifactsSHA256, EvidenceSHA256: hashes["source_fence"], OldExecutionStopped: true, ProviderRequestsDrained: true}))
		mark("source-fenced")
		must(session.Create(ctx, journalID))
	} else {
		j, e = session.Journal(ctx, journalID)
		must(e)
		if j.Phase != "created" || j.Target.RuntimeID == "" || len(j.Target.TokenCiphertext) == 0 || j.TaskCount != 1 || j.AppID != appID {
			panic("exact acknowledged replacement required")
		}
		for role, h := range hashes {
			if j.Artifacts[role] != h {
				panic("frozen artifact changed")
			}
		}
	}

	mark("replacement-created")
	j, e = session.Journal(ctx, journalID)
	must(e)
	g, e := session.TargetClient(ctx, journalID, cfg.ProxyURL)
	must(e)
	detach := attach(ctx, g, j.Target.RuntimeID)
	defer func() { detach() }()
	must(g.QuiesceWorkspace(ctx))
	mark("replacement-quiesced")
	if cmd != "verify-imported" {
		before, e := g.Status(ctx)
		must(e)
		must(g.ImportPrivateWorkspaceFile(ctx, a, an))
		await(ctx, g, func(s *rt.Status) bool { return !s.Runtimed.BootedAt.Equal(before.Runtimed.BootedAt) })
		detach()
		detach = attach(ctx, g, j.Target.RuntimeID)
		must(g.QuiesceWorkspace(ctx))
		mark("workspace-imported")
		// Workspace import restarts the supervisor. Restore home after that
		// restart so startup caches cannot change the byte-preservation check.
		must(g.ImportPrivateHome(ctx, m, h, hn))
		mark("home-imported")
		must(g.ImportPrivateTaskHistory(ctx, history))
		mark("owned-task-history-imported")
	}
	ao, e := os.OpenFile(stage+"/verified-app.zip", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	must(e)
	defer ao.Close()
	must(g.ExportPrivateWorkspaceFile(ctx, ao))
	ai, e := ao.Stat()
	must(e)
	actual, e := rt.PrivateWorkspaceFileDigest(ao, ai.Size())
	must(e)
	if actual != ad {
		panic("workspace canonical digest changed")
	}
	ho, e := os.OpenFile(stage+"/verified-home.zip", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	must(e)
	defer ho.Close()
	must(g.ExportPrivateHome(ctx, m, ho))
	hi, e := ho.Stat()
	must(e)
	actual, e = rt.PrivateHomeDigest(m, ho, hi.Size())
	must(e)
	if actual != hd {
		panic("home canonical digest changed")
	}
	historyActual, e := g.ExportPrivateTaskHistory(ctx, []string{originalTask})
	must(e)
	if !reflect.DeepEqual(historyFiles(historyActual), historyExpected) {
		panic("owned task history changed")
	}
	must(os.WriteFile(stage+"/verified-history.zip", historyActual, 0600))
	mark("workspace-home-and-task-history-byte-preservation-verified")
	must(g.ApplyAppConfig(ctx, request))
	await(ctx, g, func(s *rt.Status) bool { return s.AppConfigRevision == request.Revision })
	detach()
	detach = attach(ctx, g, j.Target.RuntimeID)
	must(g.QuiesceWorkspace(ctx))
	must(g.ResumeWorkspace(ctx))
	mark("application-started")
	await(ctx, g, func(s *rt.Status) bool { return s.Preview.Status == "ready" })
	// Verify the authenticated recovered frontend before committing the stable app binding.
	raw, e := key.Open(j.Target.TokenCiphertext, j.Target.TokenNonce)
	must(e)
	var cred struct {
		Traffic string `json:"traffic_access_token"`
	}
	must(json.Unmarshal(raw, &cred))
	hc := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", cfg.ProxyURL+"/", nil)
	must(e)
	req.Host = "3000-" + j.Target.RuntimeID + ".cube.app"
	req.Header.Set("Cube-Traffic-Access-Token", cred.Traffic)
	res, e := hc.Do(req)
	must(e)
	body, e := io.ReadAll(io.LimitReader(res.Body, 65536))
	res.Body.Close()
	must(e)
	if res.StatusCode != 200 || !strings.Contains(strings.ToLower(string(body)), "brandish") {
		panic("restored Brandish frontend health failed")
	}
	save("verification.json", map[string]any{"workspace_digest_match": true, "home_digest_match": true, "authenticated_frontend_ok": true, "task_count": j.TaskCount, "config_revision": request.Revision, "original_retained": true})
	mark("authenticated-frontend-verified")

	token, e := session.AdmissionToken(ctx, j.ID)
	must(e)
	must(session.Verify(ctx, store.CubeRecoveryVerification{AdmissionToken: token, RecoveryID: j.ID, SandboxID: sandbox, AppID: appID, OldRuntimeID: oldRuntime, NewRuntimeID: j.Target.RuntimeID, TemplateID: template, ArtifactsSHA256: j.ArtifactsSHA256, ConfigFingerprint: j.ConfigFingerprint, TaskFingerprint: j.TaskFingerprint, CredentialSHA256: store.CubeRecoveryCredentialSHA(j.Target.TokenCiphertext, j.Target.TokenNonce), EvidenceSHA256: hash(stage + "/verification.json"), WorkspaceSHA256: hashes["workspace"], HomeSHA256: hashes["home"], HistorySHA256: hashes["history"], ConfigRevision: 0, Authenticated: true, WorkspaceVerified: true, HomeVerified: true, HistoryVerified: true, ConfigApplied: true, ApplicationReady: true}))
	must(session.Commit(ctx, j.ID, oldRuntime, 0))
	must(session.Close())
	mark("recovery-committed")
}
func main() {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintln(os.Stderr, "Recovery stopped:", p)
			os.Exit(1)
		}
	}()
	run()
}
