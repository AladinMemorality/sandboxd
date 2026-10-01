// Exact-incident, offline operator. Artifacts remain private on the VPS.
// Never deletes the original runtime, retries an ambiguous create, or rewrites app identity.
package main

import (
	"archive/zip"
	"bufio"
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
	"strings"
	"syscall"
	"time"
)

const stage = "/opt/baarcha-bench/myhometroc-recovery-20260929"
const sandbox = "01M37PPK85JN1K0WEMP4ZYER6C"
const appID = "01M37PPK7VDKCQMNRYKW8CCD4W"
const oldRuntime = "4fb923eade4140bb85159fa3038efd1a"
const template = "tpl-98b45d63cfcc48c5b6ba9104"
const journalID = "myhometroc-20260929"

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
	v, e := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-p", "20222", "root@127.0.0.1", cmd).Output()
	must(e)
	return v
}
func workerLease(ctx context.Context) func() {
	c := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-p", "20222", "root@127.0.0.1", `flock -n /run/lock/cube-operator-acceptance.lock sh -c 'printf "LOCKED\n"; cat >/dev/null'`)
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
	return func() { in.Close(); c.Wait() }
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
	z, e := zip.NewReader(h, hn)
	must(e)
	control := false
	version := false
	for _, f := range z.File {
		if f.Name == ".myhometroc/pgdata/postmaster.pid" {
			panic("stale PID retained")
		}
		if f.Name == ".myhometroc/pgdata/global/pg_control" {
			control = true
		}
		if f.Name == ".myhometroc/pgdata/PG_VERSION" {
			version = true
		}
	}
	if !control || !version {
		panic("PostgreSQL source files missing")
	}
	return m, a, an, h, hn, ad, hd
}
func loadAppConfig(ctx context.Context, cfg config, key *secrets.Cipher) rt.AppConfigRequest {
	db, e := store.Open(ctx, "file:"+cfg.Database+"?_journal=WAL&_busy_timeout=5000&_fk=1", cfg.Migrations)
	must(e)
	b, e := db.GetRuntimeBinding(ctx, sandbox)
	must(e)
	if b.RuntimeID != oldRuntime || b.ConfigRevision != 1 {
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
	req := rt.AppConfigRequest{Env: env, Revision: sandbox + ":1"}
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
	fmt.Println("canonical archives validated")
	if cmd == "validate" {
		save("go-validation.json", map[string]any{"valid": true, "workspace_digest": ad, "home_digest": hd})
		return
	}
	if cmd != "preflight" && cmd != "run" && cmd != "recover-rejected" && cmd != "adopt-ack" && cmd != "verify-imported" {
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
	if cmd == "run" || cmd == "recover-rejected" || cmd == "adopt-ack" || cmd == "verify-imported" {
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
	session, e := recovery.OpenPinned(ctx, cfg.Database, cfg.Migrations, key, cfg.Provider, cfg.Policy, cfg.MasterURL)
	must(e)
	defer session.Close()
	// Opening a second store is deliberately avoided while Session checks /proc.
	// Load from the controller backup, whose bytes were captured after stop.
	frozen := cfg
	if cmd == "run" || cmd == "recover-rejected" || cmd == "adopt-ack" || cmd == "verify-imported" {
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
	worker(ctx, `python3 -c 'import pathlib,json,os,hashlib,subprocess
r=pathlib.Path("/data/cube-recovery/myhometroc-20260929");p=json.loads((r/"metadata/plan.json").read_text());source=pathlib.Path(p["current_disk"]["FilePath"]);st=source.stat();assert not source.is_symlink()
for proc in pathlib.Path("/proc").iterdir():
 if not proc.name.isdigit():continue
 try:fds=list((proc/"fd").iterdir())
 except OSError:continue
 for fd in fds:
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
	paths := map[string]string{"native_backup": stage + "/native/current.ext4", "controller_backup": stage + "/controller-before.db", "workspace": a.Name(), "home": h.Name(), "merged_home": stage + "/home.tar", "home_manifest": stage + "/converted/home-manifest.json", "source_fence": stage + "/fence.json"}
	hashes := map[string]string{}
	for role, p := range paths {
		hashes[role] = hash(p)
	}
	plan := store.CubeRecoveryPlan{ID: journalID, SandboxID: sandbox, ExpectedRuntimeID: oldRuntime, TargetTemplateID: template, TargetDomain: "cube.app", ExpectedConfigRevision: 1, Artifacts: hashes, ArtifactPaths: paths}

	var j *store.CubeRecoveryJournal
	if cmd == "run" {
		must(session.Begin(ctx, plan))
		mark("journal-held")
		j, e = session.Journal(ctx, journalID)
		must(e)
		if j.TaskCount != 0 || j.AppID != appID {
			panic("unexpected app/task identity")
		}
		must(session.Fence(ctx, store.CubeRecoveryFence{RecoveryID: j.ID, OldRuntimeID: oldRuntime, ArtifactsSHA256: j.ArtifactsSHA256, EvidenceSHA256: hashes["source_fence"], OldExecutionStopped: true, ProviderRequestsDrained: true}))
		mark("source-fenced")
		must(session.Create(ctx, journalID))
 } else if cmd=="verify-imported" {
  j,e=session.Journal(ctx,journalID);must(e)
  var remote cube.Sandbox;readJSON(stage+"/manual-create-ack.PRIVATE.json",&remote)
  if j.Phase!="created"||j.Target.RuntimeID!=remote.SandboxID||len(j.Target.TokenCiphertext)==0||j.TaskCount!=0||j.AppID!=appID{panic("exact imported target required")}
  for role,h:=range hashes{if j.Artifacts[role]!=h{panic("frozen artifact changed")}}
 } else if cmd == "adopt-ack" {
		j, e = session.Journal(ctx, journalID)
		must(e)
		var remote cube.Sandbox
		readJSON(stage+"/manual-create-ack.PRIVATE.json", &remote)
		if j.Phase != "created" || j.Target.RuntimeID != remote.SandboxID || len(j.Target.TokenCiphertext) != 0 || j.TaskCount != 0 || j.AppID != appID {
			panic("exact acknowledged uncredentialed target required")
		}
		for role, h := range hashes {
			if j.Artifacts[role] != h {
				panic("frozen artifact changed")
			}
		}
		must(session.AdoptWithIngress(ctx, journalID, remote.SandboxID, remote.TrafficAccessToken, cfg.ProxyURL))
	} else {
		j, e = session.Journal(ctx, journalID)
		must(e)
		if j.Phase != "creating" || j.Target.RuntimeID != "" || j.TaskCount != 0 || j.AppID != appID || j.Old.RuntimeID != oldRuntime {
			panic("exact rejected creation journal required")
		}
		for role, h := range hashes {
			if j.Artifacts[role] != h {
				panic("frozen artifact changed")
			}
		}
		var evidence struct {
			Bound     bool   `json:"request_bound"`
			Rejected  bool   `json:"terminal_no_resource"`
			Matches   int    `json:"matching_targets"`
			All       bool   `json:"all_nodes_scanned"`
			Token     string `json:"operation_token"`
			Hash      string `json:"request_sha256"`
			RequestID string `json:"request_id"`
		}
		readJSON(stage+"/native-create-rejection.private.json", &evidence)
		if !evidence.Bound || !evidence.Rejected || !evidence.All || evidence.Matches != 0 || evidence.Token != j.OperationToken || evidence.Hash != j.RequestSHA256 || evidence.RequestID != "229e031d-4777-4c50-aec5-c68037568bc5" {
			panic("conclusive bound native rejection required")
		}
		listing := string(worker(ctx, "cubemastercli -a 127.0.0.1 list --all --wide"))
		if !strings.Contains(listing, "NODES_SCANNED    2/2") || strings.Contains(listing, j.OperationToken) || strings.Contains(listing, journalID) {
			panic("complete empty exact-operation inventory required")
		}
		clock, e := cube.ReadStorageClock()
		must(e)
		observation, e := cube.ReadStorageObservation(*cfg.Policy.StorageGuard, clock)
		must(e)
		if observation.InnerFreeBytes < cube.StorageReserve+cube.StorageGrant || observation.OuterFreeBytes < cube.StorageReserve+cube.StorageGrant {
			panic("recovery storage reserve unavailable")
		}
		plain, e := key.Open(j.PlannedCiphertext, j.PlannedNonce)
		must(e)
		var cred struct {
			Supervisor string `json:"supervisor_token"`
		}
		must(json.Unmarshal(plain, &cred))
		if fmt.Sprintf("%x", sha256.Sum256([]byte(cred.Supervisor))) != j.SupervisorSHA256 {
			panic("planned supervisor credential changed")
		}
		input := cube.CreateRequest{TemplateID: template, TimeoutSeconds: 3600, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{AllowOut: []string{}, DenyOut: []string{"0.0.0.0/0"}}, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": cred.Supervisor}, Metadata: map[string]string{"sandboxd_id": sandbox, "sandboxd_app_id": appID, "sandboxd_recovery_id": journalID, "sandboxd_admission_operation": j.OperationToken}, DistributionScope: []string{cfg.Policy.NodeID}}
		raw, e := json.Marshal(input)
		must(e)
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != j.RequestSHA256 {
			panic("reviewed creation request hash changed")
		}
		// Explicit operator reconciliation of a proven terminal pre-allocation rejection.
		// The original charged journal/operation is retained. This command has one
		// exclusive durable attempt marker and never retries an uncertain result.
		intent, e := os.OpenFile(stage+"/manual-create-intent.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		must(e)
		proof, e := json.Marshal(map[string]string{"request_sha256": j.RequestSHA256, "rejection_evidence_sha256": hash(stage + "/native-create-rejection.private.json")})
		must(e)
		_, e = intent.Write(proof)
		must(e)
		must(intent.Sync())
		must(intent.Close())
		api, e := cube.New(cfg.Provider)
		must(e)
		remote, e := api.Create(ctx, input)
		must(e)
		save("manual-create-ack.PRIVATE.json", remote)
		must(session.AdoptWithIngress(ctx, journalID, remote.SandboxID, remote.TrafficAccessToken, cfg.ProxyURL))
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
 if cmd!="verify-imported" {
	must(g.ImportPrivateHome(ctx, m, h, hn))
	mark("home-imported")
	before, e := g.Status(ctx)
	must(e)
	must(g.ImportPrivateWorkspaceFile(ctx, a, an))
	await(ctx, g, func(s *rt.Status) bool { return !s.Runtimed.BootedAt.Equal(before.Runtimed.BootedAt) })
	detach()
	detach = attach(ctx, g, j.Target.RuntimeID)
	must(g.QuiesceWorkspace(ctx))
	mark("workspace-imported")
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
	mark("app-and-database-byte-preservation-verified")
	must(g.ApplyAppConfig(ctx, request))
	await(ctx, g, func(s *rt.Status) bool { return s.AppConfigRevision == request.Revision })
	detach()
	detach = attach(ctx, g, j.Target.RuntimeID)
	must(g.QuiesceWorkspace(ctx))
	must(g.ResumeWorkspace(ctx))
	mark("application-started")
	await(ctx, g, func(s *rt.Status) bool { return s.Preview.Status == "ready" })
	// Read-only health endpoint executes SELECT 1 in the restored PostgreSQL.
	raw, e := key.Open(j.Target.TokenCiphertext, j.Target.TokenNonce)
	must(e)
	var cred struct {
		Traffic string `json:"traffic_access_token"`
	}
	must(json.Unmarshal(raw, &cred))
	hc := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", cfg.ProxyURL+"/api/health", nil)
	must(e)
	req.Host = "3000-" + j.Target.RuntimeID + ".cube.app"
	req.Header.Set("Cube-Traffic-Access-Token", cred.Traffic)
	res, e := hc.Do(req)
	must(e)
	body, e := io.ReadAll(io.LimitReader(res.Body, 4096))
	res.Body.Close()
	must(e)
	var health struct {
		OK       bool   `json:"ok"`
		Database string `json:"database"`
	}
	must(json.Unmarshal(body, &health))
	if res.StatusCode != 200 || !health.OK || health.Database != "PostgreSQL" {
		panic("restored database health failed")
	}
	save("verification.json", map[string]any{"workspace_digest_match": true, "home_digest_match": true, "postgresql_query_ok": true, "task_count": j.TaskCount, "config_revision": request.Revision, "original_retained": true})
	mark("application-and-postgresql-verified")
	token, e := session.AdmissionToken(ctx, j.ID)
	must(e)
	must(session.Verify(ctx, store.CubeRecoveryVerification{AdmissionToken: token, RecoveryID: j.ID, SandboxID: sandbox, AppID: appID, OldRuntimeID: oldRuntime, NewRuntimeID: j.Target.RuntimeID, TemplateID: template, ArtifactsSHA256: j.ArtifactsSHA256, ConfigFingerprint: j.ConfigFingerprint, TaskFingerprint: j.TaskFingerprint, CredentialSHA256: store.CubeRecoveryCredentialSHA(j.Target.TokenCiphertext, j.Target.TokenNonce), EvidenceSHA256: hash(stage + "/verification.json"), WorkspaceSHA256: hashes["workspace"], HomeSHA256: hashes["home"], HistorySHA256: j.TaskFingerprint, ConfigRevision: 1, Authenticated: true, WorkspaceVerified: true, HomeVerified: true, HistoryVerified: true, ConfigApplied: true, ApplicationReady: true}))
	must(session.Commit(ctx, j.ID, oldRuntime, 1))
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
