// Exact-incident offline recovery. Never reuses a failed memory snapshot or
// deletes the original disk. Session owns the native maintenance/SQLite fence.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/recovery"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const root = "/opt/baarcha/operations/vps-50-profiles-20261008"
const stage = root + "/exited-recovery-01"
const sandbox = "01M24GEQ88YGHHH3WPC9WD9DY0"
const appID = "01M24GEQ88RFYJBJ86KYW5BVN6"
const oldRuntime = "38a1bd34cfa54a9aa47b4b121d035e46"
const template = "tpl-3c4e83ebf6c641f293c0e816"
const journalID = "vps-exited-20261009"
const prepared = root + "/recovery-prepared-exited-20261009/" + sandbox

type config struct {
	Database, Migrations, KeyFile, MasterURL, ProxyURL, ProtectedCIDRs, ProtectedDomains string
	Provider                                                                             cube.Config
	Policy                                                                               cube.AdmissionConfig
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func read(path string, v any) { b, e := os.ReadFile(path); must(e); must(json.Unmarshal(b, v)) }
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
	must(os.WriteFile(filepath.Join(stage, name), b, 0600))
}
func mark(s string) {
	save("progress.json", map[string]any{"step": s, "at": time.Now().UTC()})
	fmt.Println(s)
}
func main() {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintln(os.Stderr, "recovery stopped:", p)
			os.Exit(1)
		}
	}()
	run()
}
func run() {
	if os.Geteuid() != 0 || len(os.Args) != 2 {
		panic("native root and preflight/run/continue required")
	}
	mode := os.Args[1]
	if mode != "preflight" && mode != "run" && mode != "continue" && mode != "adopt" {
		panic("unsupported mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	var cfg config
	read(stage+"/operator-config.PRIVATE.json", &cfg)
	must(cfg.Policy.RequireStorageGuard())
	if cfg.Policy.NodeID != "10.0.2.15" || cfg.Provider.APIURL != "http://127.0.0.1:20300" || cfg.MasterURL != "http://10.254.240.1:18089" || cfg.ProxyURL != "http://127.0.0.1:20080" {
		panic("exact VPS endpoints required")
	}
	policy, e := egress.OperatorPolicy(cfg.ProtectedCIDRs, cfg.ProtectedDomains)
	must(e)
	policy.AllowedHosts = []string{"registry.npmjs.org"}
	policy.Ports = []uint16{443}
	must(policy.Validate())
	key, e := secrets.Load("", cfg.KeyFile)
	must(e)
	var source map[string]any
	read(prepared+"/export-result.PRIVATE.json", &source)
	if source["sandbox_id"] != sandbox || source["runtime_id"] != oldRuntime || len(source["task_ids"].([]any)) != 0 {
		panic("source identity changed")
	}
	paths := map[string]string{"workspace": prepared + "/workspace.zip", "home": prepared + "/home.zip", "history": prepared + "/history.zip", "source_archive": "/var/backups/baarcha-vps-exited-20261009/20261009T144656Z/sandboxes/" + sandbox + "/home.tar.gz", "native_manifest": stage + "/native-backup.json"}
	hashes := map[string]string{}
	for k, p := range paths {
		hashes[k] = hash(p)
	}
	var native struct{ Backup, SHA256 string }
	read(stage+"/native-backup.json", &native)
	paths["native_backup"] = native.Backup
	hashes["native_backup"] = native.SHA256
	frozen := stage + "/controller-before.db"
	if mode == "preflight" {
		frozen = stage + "/preflight.db"
		cfg.Database = frozen
	}
	// This read-only handle is closed before acquiring the exclusive Session.
	db, e := store.Open(ctx, "file:"+frozen+"?_journal=WAL&_busy_timeout=5000&_fk=1", cfg.Migrations)
	must(e)
	binding, e := db.GetRuntimeBinding(ctx, sandbox)
	must(e)
	if binding.RuntimeID != oldRuntime || binding.ConfigRevision != 0 {
		panic("source binding changed")
	}
	entries, e := appenv.For(ctx, db, key, appID)
	must(e)
	must(db.Close())
	env := map[string]string{}
	for _, entry := range entries {
		k, v, _ := strings.Cut(entry, "=")
		env[k] = v
	}
	if mode == "preflight" {
		l, e := maintenance.Acquire(cfg.Database, false)
		must(e)
		must(l.Close())
	}
	session, e := recovery.OpenPinnedWorker(ctx, cfg.Database, cfg.Migrations, key, cfg.Provider, cfg.Policy, cfg.MasterURL, "vps")
	must(e)
	defer session.Close()
	if mode == "preflight" {
		mark("preflight-passed")
		return
	}
	var fence struct {
		OldRuntimeID                                 string
		OldExecutionStopped, ProviderRequestsDrained bool
		Expires                                      int64
	}
	read(stage+"/fence.json", &fence)
	if fence.OldRuntimeID != oldRuntime || !fence.OldExecutionStopped || !fence.ProviderRequestsDrained || fence.Expires < time.Now().Unix() {
		panic("fresh independent source fence required")
	}
	paths["controller_backup"] = frozen
	hashes["controller_backup"] = hash(frozen)
	paths["source_fence"] = stage + "/fence.json"
	hashes["source_fence"] = hash(paths["source_fence"])
	if mode == "run" {
		must(session.Begin(ctx, store.CubeRecoveryPlan{ID: journalID, SandboxID: sandbox, ExpectedRuntimeID: oldRuntime, TargetTemplateID: template, TargetDomain: "cube.app", ExpectedConfigRevision: 0, Artifacts: hashes, ArtifactPaths: paths}))
		mark("journal-held")
		j, e := session.Journal(ctx, journalID)
		must(e)
		if j.AppID != appID || j.TaskCount != 0 {
			panic("identity mismatch")
		}
		must(session.Fence(ctx, store.CubeRecoveryFence{RecoveryID: journalID, OldRuntimeID: oldRuntime, ArtifactsSHA256: j.ArtifactsSHA256, EvidenceSHA256: hashes["source_fence"], OldExecutionStopped: true, ProviderRequestsDrained: true}))
		mark("source-fenced")
		must(session.Create(ctx, journalID))
		mark("replacement-created")
	}
	if mode == "adopt" {
		var existing cube.Sandbox
		read(stage+"/adopt.PRIVATE.json", &existing)
		if existing.SandboxID != "d95e537c7aee4dda96d9bbc13f61d4f9" || existing.TrafficAccessToken == "" {
			panic("exact retained replacement required")
		}
		must(session.AdoptWithIngress(ctx, journalID, existing.SandboxID, existing.TrafficAccessToken, cfg.ProxyURL))
		mark("replacement-adopted")
	}
	j, e := session.Journal(ctx, journalID)
	must(e)
	if j.Phase != "created" || j.AppID != appID || j.TaskCount != 0 {
		panic("acknowledged replacement required")
	}
	for role, h := range hashes {
		if j.Artifacts[role] != h {
			panic("frozen artifact changed")
		}
	}
	g, e := session.TargetClient(ctx, journalID, cfg.ProxyURL)
	must(e)
	// The authenticated import restarts runtimed. Reconnect only this target's
	// reverse channel so dependency downloads can continue after that restart.
	sub, detach := context.WithCancel(ctx)
	defer detach()
	go func() {
		for sub.Err() == nil {
			conn, e := g.OpenEgressChannel(sub)
			if e == nil {
				_ = egress.RunHost(sub, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: j.Target.RuntimeID, Generation: journalID}, Policy: policy})
			}
			select {
			case <-sub.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	raw, e := key.Open(j.Target.TokenCiphertext, j.Target.TokenNonce)
	must(e)
	var cred struct {
		Supervisor string `json:"supervisor_token"`
		Traffic    string `json:"traffic_access_token"`
	}
	must(json.Unmarshal(raw, &cred))
	request := map[string]any{"worker": "vps", "id": journalID, "sandbox_id": sandbox, "runtime_id": j.Target.RuntimeID, "headers": map[string]string{"Host": "3031-" + j.Target.RuntimeID + ".cube.app", "Authorization": "Bearer " + cred.Supervisor, "cube-traffic-access-token": cred.Traffic}, "source": source, "receipts": source["artifacts"], "env": env, "config_revision": 0, "web_port": 3000}
	save("worker-job.PRIVATE.json", request)
	restoreMode := mode
	if mode == "adopt" {
		restoreMode = "run"
	}
	cmd := exec.CommandContext(ctx, "python3", stage+"/restore.py", restoreMode)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	must(cmd.Run())
	mark("content-and-application-verified")
	var proof struct {
		WorkspaceVerified, HomeVerified, HistoryVerified, ConfigApplied, ApplicationReady bool
		SandboxID, RuntimeID                                                              string
	}
	read(stage+"/verified.json", &proof)
	if !proof.WorkspaceVerified || !proof.HomeVerified || !proof.HistoryVerified || !proof.ConfigApplied || !proof.ApplicationReady || proof.SandboxID != sandbox || proof.RuntimeID != j.Target.RuntimeID {
		panic("incomplete proof")
	}
	status, e := g.Status(ctx)
	must(e)
	if status.ActiveTask != nil || status.AppConfigRevision != sandbox+":0" {
		panic("unexpected final guest state")
	}
	token, e := session.AdmissionToken(ctx, journalID)
	must(e)
	must(session.Verify(ctx, store.CubeRecoveryVerification{AdmissionToken: token, RecoveryID: journalID, SandboxID: sandbox, AppID: appID, OldRuntimeID: oldRuntime, NewRuntimeID: j.Target.RuntimeID, TemplateID: template, ArtifactsSHA256: j.ArtifactsSHA256, ConfigFingerprint: j.ConfigFingerprint, TaskFingerprint: j.TaskFingerprint, CredentialSHA256: store.CubeRecoveryCredentialSHA(j.Target.TokenCiphertext, j.Target.TokenNonce), EvidenceSHA256: hash(stage + "/verified.json"), WorkspaceSHA256: hashes["workspace"], HomeSHA256: hashes["home"], HistorySHA256: j.TaskFingerprint, ConfigRevision: 0, Authenticated: true, WorkspaceVerified: true, HomeVerified: true, HistoryVerified: true, ConfigApplied: true, ApplicationReady: true}))
	must(session.Commit(ctx, journalID, oldRuntime, 0))
	must(session.Close())
	save("complete.json", map[string]any{"restored": true, "sandbox_id": sandbox, "runtime_id": j.Target.RuntimeID, "original_retained": true, "worker": "vps", "model_calls": false})
	mark("recovery-committed")
}
