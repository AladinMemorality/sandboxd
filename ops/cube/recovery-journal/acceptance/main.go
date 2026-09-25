// Explicitly synthetic journal acceptance; never points at a production database.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/recovery"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const syntheticTask = "01M2QTJAM3F62CJ6ZQMEPE8H22"
const taskResult = `{"id":"01M2QTJAM3F62CJ6ZQMEPE8H22","status":"succeeded","checkpoint_id":"synthetic-journal-only"}`

type journalHandoff struct {
	Purpose  string `json:"purpose"`
	Fixture  string `json:"fixture"`
	OldID    string `json:"old_provider_id"`
	Machine  string `json:"worker_machine_id"`
	Archive  string `json:"current_archive_sha256"`
	Evidence string `json:"recovery_evidence_sha256"`
	Expires  int64  `json:"expires_at"`
}

func checkHandoff(h journalHandoff, old escrow, now int64) error {
	hex64 := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if old.Guest == nil || old.Guest.TemplateID != template || h.Purpose != "DISPOSABLE_CURRENT_DISK_JOURNAL" || h.Fixture != old.Fixture || h.OldID != old.Guest.SandboxID || h.Machine != old.WorkerMachineID || h.Expires <= now || h.Expires > now+1200 || !hex64.MatchString(h.Archive) || !hex64.MatchString(h.Evidence) {
		return errors.New("fresh exact owned journal handoff required")
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(old.Fixture) || old.Latest.Fixture != old.Fixture || old.Latest.Phase != "latest" || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(old.Latest.Nonce) || old.Latest.Nonce == old.Baseline.Nonce {
		return errors.New("original acknowledged latest fixture required")
	}
	return nil
}
func privateStage(path string, prefix string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasPrefix(path, prefix) {
		return errors.New("invalid isolated fixture path")
	}
	for cur := path; cur != "/"; cur = filepath.Dir(cur) {
		info, e := os.Lstat(cur)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("directory chain must not contain links")
		}
	}
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		return errors.New("private root stage required")
	}
	return nil
}
func fixtureHistory(ctx context.Context, stage string) []byte {
	root := filepath.Join(stage, "synthetic-task-source")
	must(os.Mkdir(root, 0700))
	dir := filepath.Join(root, syntheticTask)
	must(os.Mkdir(dir, 0700))
	must(durable(filepath.Join(dir, "events.jsonl"), []byte("{\"type\":\"done\",\"synthetic_journal_only\":true}\n"), true))
	must(durable(filepath.Join(dir, "result.json"), []byte(taskResult), true))
	data, e := rt.ExportPrivateTaskHistory(ctx, root, []string{syntheticTask})
	must(e)
	must(durable(filepath.Join(stage, "synthetic-history.zip"), data, true))
	return data
}
func fixturePolicy() cube.AdmissionConfig {
	return cube.AdmissionConfig{MaxActive: 4, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{template: {CPUCount: 2, MemoryMB: 2048}}}
}
func openFixture(ctx context.Context, path, migrations string) *store.Store {
	db, e := store.Open(ctx, "file:"+path+"?_journal=WAL&_busy_timeout=5000&_fk=1", migrations)
	must(e)
	return db
}
func seedDatabase(ctx context.Context, path, migrations string, key *secrets.Cipher, old escrow, appID, sandboxID, owner string) {
	if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
		panic("refuse existing controller database")
	}
	db := openFixture(ctx, path, migrations)
	defer db.Close()
	client, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture-only"})
	must(e)
	must(client.ConfigureAdmission(ctx, db, fixturePolicy()))
	app := &store.App{ID: appID, OwnerToken: owner, Name: "SYNTHETIC recovery journal", ExternalUserID: sql.NullString{String: "journal-fixture-owner", Valid: true}, ExternalProjectID: sql.NullString{String: "journal-fixture-project", Valid: true}, RuntimePreset: sql.NullString{String: "node-postgres", Valid: true}}
	must(db.CreateApp(ctx, app))
	raw, e := json.Marshal(map[string]string{"supervisor_token": old.Supervisor, "traffic_access_token": old.Guest.TrafficAccessToken})
	must(e)
	cipher, nonce, e := key.Seal(raw)
	must(e)
	lease, e := db.AdmissionBegin(ctx, "app:"+appID, "", template, "create", "synthetic-original-lease")
	must(e)
	must(db.AdmissionFinish(ctx, lease, old.Guest.SandboxID, "active"))
	must(db.Create(ctx, &store.Sandbox{ID: sandboxID, AppID: sql.NullString{String: appID, Valid: true}, Status: "running", RuntimeProvider: "cube", IdlePolicy: "sleep", RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: old.Guest.SandboxID, TemplateID: template, Domain: "cube.app", TokenCiphertext: cipher, TokenNonce: nonce}}))
	must(db.CreateAppConfig(ctx, &store.AppConfig{ID: "synthetic-config", AppID: appID, Key: "JOURNAL_FIXTURE", AccessPolicy: "runtime_access", ValuePlaintext: sql.NullString{String: old.Fixture, Valid: true}}))
	must(db.CreateTask(ctx, &store.Task{TaskID: syntheticTask, SandboxID: sandboxID, Agent: "synthetic-no-model", Prompt: "Synthetic task-history transport fixture; never ran on original crashed guest"}))
	must(db.FinishTask(ctx, syntheticTask, "succeeded", taskResult))
}
func archiveRoundtrip(c *coordinator, stage string, manifest rt.HomeManifest, app *os.File, appSize int64, home *os.File, homeSize int64, history []byte) {
	expectedApp, e := rt.PrivateWorkspaceFileDigest(app, appSize)
	must(e)
	expectedHome, e := rt.PrivateHomeDigest(manifest, home, homeSize)
	must(e)
	out, e := os.OpenFile(filepath.Join(stage, "verified-app.zip"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	must(e)
	defer out.Close()
	must(c.guest.ExportPrivateWorkspaceFile(c.ctx, out))
	info, e := out.Stat()
	must(e)
	gotApp, e := rt.PrivateWorkspaceFileDigest(out, info.Size())
	must(e)
	if gotApp != expectedApp {
		panic("workspace import/export digest changed")
	}
	h, e := os.OpenFile(filepath.Join(stage, "verified-home.zip"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	must(e)
	defer h.Close()
	must(c.guest.ExportPrivateHome(c.ctx, manifest, h))
	info, e = h.Stat()
	must(e)
	gotHome, e := rt.PrivateHomeDigest(manifest, h, info.Size())
	must(e)
	if gotHome != expectedHome {
		panic("home import/export digest changed")
	}
	gotHistory, e := c.guest.ExportPrivateTaskHistory(c.ctx, []string{syntheticTask})
	must(e)
	a, e := rt.PrivateWorkspaceDigest(history)
	must(e)
	b, e := rt.PrivateWorkspaceDigest(gotHistory)
	must(e)
	if a != b {
		panic("synthetic canonical task history changed")
	}
	c.report["quiesced_app_home_digests_match"] = true
	c.report["synthetic_task_history_roundtrip"] = true
}
func waitConfig(c *coordinator, revision string) {
	for i := 0; i < 80; i++ {
		s, e := c.guest.Status(c.ctx)
		if e == nil && s.AppConfigRevision == revision {
			return
		}
		select {
		case <-c.ctx.Done():
			panic("config deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
	panic("runtime did not acknowledge frozen config revision")
}
func runJournal() (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("owned journal acceptance stopped; retain isolated journal and target for operator review")
		}
	}()
	if len(os.Args) != 5 || os.Args[1] != "run" || os.Geteuid() != 0 {
		return errors.New("usage: journal-acceptance run ORIGINAL_CRASH_STAGE CONVERTED_DIR NEW_PRIVATE_STAGE")
	}
	oldDir, input, stage := os.Args[2], os.Args[3], os.Args[4]
	must(privateStage(oldDir, "/opt/baarcha-bench/cube-crash-"))
	must(privateStage(input, "/opt/baarcha-bench/"))
	must(privateStage(stage, "/opt/baarcha-bench/cube-journal-"))
	if _, e := os.Lstat(filepath.Join(stage, "intent.private.json")); !errors.Is(e, os.ErrNotExist) {
		panic("prior intent exists; no implicit retry")
	}
	lock, e := os.OpenFile("/run/lock/cube-operator-acceptance.lock", os.O_RDWR|os.O_CREATE, 0600)
	must(e)
	defer lock.Close()
	must(syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	release := holdWorkerLease(ctx)
	defer release()
	var old escrow
	must(privateJSON(filepath.Join(oldDir, "escrow.private.json"), &old))
	var handoff journalHandoff
	must(privateJSON(filepath.Join(stage, "handoff.json"), &handoff))
	must(checkHandoff(handoff, old, time.Now().Unix()))
	var sourceReport struct {
		Checkpoint bool      `json:"checkpoint_ready"`
		Latest     marker    `json:"latest"`
		Ack        time.Time `json:"latest_acknowledged_at"`
		Native     bool      `json:"post_power_loss_latest_app_home_sql_preserved"`
	}
	must(privateJSON(filepath.Join(oldDir, "report.json"), &sourceReport))
	if !sourceReport.Checkpoint || sourceReport.Latest != old.Latest || sourceReport.Ack.IsZero() || sourceReport.Native {
		panic("acknowledged failed-native source required")
	}
	var converted struct {
		SourceSHA string `json:"source_archive_sha256"`
		AppSHA    string `json:"app_sha256"`
		HomeSHA   string `json:"home_sha256"`
		Validated bool   `json:"go_import_contract_validated"`
		PID       bool   `json:"postmaster_pid_preserved"`
	}
	must(privateJSON(filepath.Join(input, "conversion.json"), &converted))
	if converted.SourceSHA != handoff.Archive || !converted.Validated || !converted.PID {
		panic("unbound converted archives")
	}
	app, appSize := openArchive(filepath.Join(input, "app.zip"), rt.MaxPrivateWorkspaceStreamBytes)
	defer app.Close()
	home, homeSize := openArchive(filepath.Join(input, "home.zip"), rt.MaxPrivateHomeBytes)
	defer home.Close()
	if hashFile(app) != converted.AppSHA || hashFile(home) != converted.HomeSHA {
		panic("archive hashes differ")
	}
	var manifest rt.HomeManifest
	must(privateJSON(filepath.Join(input, "home-manifest.json"), &manifest))
	_, e = rt.PrivateWorkspaceFileDigest(app, appSize)
	must(e)
	_, e = rt.PrivateHomeDigest(manifest, home, homeSize)
	must(e)
	az, e := zip.NewReader(app, appSize)
	must(e)
	hz, e := zip.NewReader(home, homeSize)
	must(e)
	must(validateArchiveMarker(az, "crash-fixture-data/marker.json", old.Latest))
	must(validateArchiveMarker(hz, ".cube-crash-fixture/marker.json", old.Latest))
	token, e := zipMember(az, "crash-fixture-token", 128)
	must(e)
	probe, e := zipMember(az, "probe.mjs", 1<<20)
	must(e)
	originalProbe, e := os.ReadFile(filepath.Join(oldDir, "probe.mjs"))
	must(e)
	if string(token) != old.Capability || hashBytes(probe) != hashBytes(originalProbe) {
		panic("original readonly probe/capability mismatch")
	}
	hostname, e := worker(ctx, "hostname")
	must(e)
	machine, e := worker(ctx, "cat /etc/machine-id")
	must(e)
	dataUUID, e := worker(ctx, "findmnt -n -o UUID /data")
	must(e)
	boot := bootID(ctx)
	if hostname != "baarcha-cube-worker-01" || machine != old.WorkerMachineID || dataUUID != old.DataUUID || boot == old.BootID {
		panic("worker fence identity mismatch")
	}
	inventory, e := worker(ctx, "cubemastercli -a 127.0.0.1 list --all --wide")
	must(e)
	tasks, e := worker(ctx, "timeout 5s ctr --address /data/cubelet/cubelet.sock --namespace default tasks list 2>/dev/null")
	must(e)
	if !emptyCubeTasks(tasks) {
		panic("owned fixture requires no live worker tasks")
	}
	env, e := os.ReadFile("/opt/baarcha-cube/worker-01/staging/cube-install.env")
	must(e)
	apiKey := ""
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "CUBE_API_KEY=") {
			apiKey = strings.Trim(strings.TrimPrefix(line, "CUBE_API_KEY="), "\"'")
		}
	}
	provider := cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: apiKey}
	api, e := cube.New(provider)
	must(e)
	_, e = api.Get(ctx, old.Guest.SandboxID)
	sourceBranch, branchErr := oldStateBranch(e, inventory, old.Guest.SandboxID)
	must(branchErr)
	purpose := "OWNED_CURRENT_DISK_MISSING_PROVIDER"
	if sourceBranch == "unavailable-retained" {
		purpose = "OWNED_CURRENT_DISK_RETAINED_PROVIDER"
	}
	must(validateSourceFiles(stage, handoff.Evidence, old, converted.SourceSHA, boot, purpose))
	var upstream *cube.APIError
	fixture := nonce(16)
	appID, sandboxID, owner := "journal-app-"+fixture, "journal-sandbox-"+fixture, nonce(32)
	database := filepath.Join(stage, "isolated-controller.db")
	migrations := "../../migrations"
	history := fixtureHistory(ctx, stage)
	key, e := secrets.Load("", filepath.Join(stage, "isolated-controller.key"))
	must(e)
	seedDatabase(ctx, database, migrations, key, old, appID, sandboxID, owner)
	// This is the newly seeded synthetic controller backup, not a pre-crash controller backup.
	before, e := os.ReadFile(database)
	must(e)
	must(durable(filepath.Join(stage, "synthetic-controller-before.db"), before, true))
	daemon, e := maintenance.Acquire(database, false)
	must(e)
	must(daemon.Close())
	session, e := recovery.Open(ctx, database, migrations, key, provider, fixturePolicy())
	must(e)
	defer session.Close()
	c := &coordinator{ctx: ctx, stage: stage, cube: api, saved: escrow{Fixture: fixture, Capability: old.Capability, Latest: old.Latest}, report: map[string]any{"production_accepted": false, "production_binding_changed": false, "original_crash_task_history_present": false, "synthetic_history_only": true, "old_provider_id": old.Guest.SandboxID}}
	defer c.detach()
	defer func() {
		if p := recover(); p != nil {
			c.report["failed_after_stage"] = c.report["stage"]
			c.report["target_cleanup_requires_journal_review"] = true
			c.report["acknowledged_target_id_available"] = c.saved.Guest != nil
			c.record("journal-acceptance-failed")
			panic(p)
		}
	}()
	save(filepath.Join(stage, "intent.private.json"), map[string]any{"fixture": fixture, "app_id": appID, "sandbox_id": sandboxID, "old_runtime_id": old.Guest.SandboxID}, true)
	derivedPath := filepath.Join(stage, "home-without-stale-pid.zip")
	_, e = deriveHome(home, homeSize, derivedPath, manifest, sourceReport.Ack)
	must(e)
	derived, derivedSize := openArchive(derivedPath, rt.MaxPrivateHomeBytes)
	defer derived.Close()
	plan := store.CubeRecoveryPlan{ID: "journal-" + fixture, SandboxID: sandboxID, ExpectedRuntimeID: old.Guest.SandboxID, TargetTemplateID: template, TargetDomain: "cube.app", ExpectedConfigRevision: 1, Artifacts: map[string]string{"native_backup": handoff.Archive, "controller_backup": hashBytes(before), "workspace": converted.AppSHA, "home": hashFile(derived), "history": hashBytes(history)}, ArtifactPaths: map[string]string{"native_backup": filepath.Join(stage, "export-report.json"), "controller_backup": filepath.Join(stage, "synthetic-controller-before.db"), "workspace": filepath.Join(input, "app.zip"), "home": derivedPath, "history": filepath.Join(stage, "synthetic-history.zip")}}
	// The retained native archive is pinned by its actual path in the private handoff receipt.
	var nativePath struct {
		ArchivePath     string `json:"retained_home_tar_path"`
		CurrentDiskPath string `json:"retained_current_disk_path"`
	}
	must(privateJSON(filepath.Join(stage, "native-archive.json"), &nativePath))
	native, nativeSize := openArchive(nativePath.ArchivePath, 20<<30)
	defer native.Close()
	if nativeSize <= 0 || hashFile(native) != handoff.Archive {
		panic("retained native archive mismatch")
	}
	plan.Artifacts["merged_home"] = handoff.Archive
	plan.ArtifactPaths["merged_home"] = nativePath.ArchivePath
	var captureManifest struct {
		Artifacts []struct {
			File  string `json:"file"`
			SHA   string `json:"sha256"`
			Bytes int64  `json:"bytes"`
		} `json:"artifacts"`
	}
	must(privateJSON(filepath.Join(stage, "rescue-input.json"), &captureManifest))
	if len(captureManifest.Artifacts) < 2 || captureManifest.Artifacts[0].File != "current.ext4" {
		panic("retained current disk evidence missing")
	}
	currentDisk, currentDiskSize := openArchive(nativePath.CurrentDiskPath, 16<<30)
	defer currentDisk.Close()
	if currentDiskSize != captureManifest.Artifacts[0].Bytes || hashFile(currentDisk) != captureManifest.Artifacts[0].SHA {
		panic("retained native current disk mismatch")
	}
	plan.Artifacts["native_backup"] = captureManifest.Artifacts[0].SHA
	plan.ArtifactPaths["native_backup"] = nativePath.CurrentDiskPath
	plan.Artifacts["original_home"] = converted.HomeSHA
	plan.ArtifactPaths["original_home"] = filepath.Join(input, "home.zip")
	must(session.Begin(ctx, plan))
	c.record("journal-held")
	if session.Commit(ctx, plan.ID, old.Guest.SandboxID, 1) == nil {
		panic("unverified commit accepted")
	}
	j, e := session.Journal(ctx, plan.ID)
	must(e)
	must(session.Fence(ctx, store.CubeRecoveryFence{RecoveryID: j.ID, OldRuntimeID: j.Old.RuntimeID, ArtifactsSHA256: j.ArtifactsSHA256, EvidenceSHA256: handoff.Evidence, OldExecutionStopped: true, ProviderRequestsDrained: true}))
	c.record("journal-fenced")
	must(session.Create(ctx, plan.ID))
	j, e = session.Journal(ctx, plan.ID)
	must(e)
	remote, e := api.Get(ctx, j.Target.RuntimeID)
	must(e)
	if remote.Metadata["sandboxd_recovery_id"] != j.ID || remote.Metadata["sandboxd_id"] != sandboxID || remote.Metadata["sandboxd_app_id"] != appID || remote.TemplateID != template || remote.CPUCount != 2 || remote.MemoryMB != 2048 {
		panic("exact journal target mismatch")
	}
	raw, e := key.Open(j.Target.TokenCiphertext, j.Target.TokenNonce)
	must(e)
	var creds struct {
		Supervisor string `json:"supervisor_token"`
		Traffic    string `json:"traffic_access_token"`
	}
	must(json.Unmarshal(raw, &creds))
	remote.TrafficAccessToken = creds.Traffic
	c.saved.Guest = remote
	c.saved.Supervisor = creds.Supervisor
	save(filepath.Join(stage, "target.private.json"), c.saved, true)
	c.guest, e = session.TargetClient(ctx, j.ID, "http://127.0.0.1:20080")
	must(e)
	c.attach()
	c.ready()
	quiesced(c)
	c.record("journal-target-quiesced")
	must(c.guest.ImportPrivateHome(ctx, manifest, derived, derivedSize))
	beforeStatus, e := c.guest.Status(ctx)
	must(e)
	must(c.guest.ImportPrivateWorkspaceFile(ctx, app, appSize))
	reexec := false
	for i := 0; i < 80; i++ {
		s, e := c.guest.Status(ctx)
		if e == nil && !s.Runtimed.BootedAt.Equal(beforeStatus.Runtimed.BootedAt) {
			reexec = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !reexec {
		panic("workspace reexec not observed")
	}
	c.attach()
	quiesced(c)
	must(c.guest.ImportPrivateTaskHistory(ctx, history))
	archiveRoundtrip(c, stage, manifest, app, appSize, derived, derivedSize, history)
	configRevision := "journal-" + fixture + "-1"
	must(c.guest.ApplyAppConfig(ctx, rt.AppConfigRequest{Env: map[string]string{"JOURNAL_FIXTURE": old.Fixture}, Revision: configRevision}))
	waitConfig(c, configRevision)
	c.attach()
	quiesced(c)
	must(c.guest.ResumeWorkspace(ctx))
	c.ready()
	// No /commit request exists in this coordinator. Compare original acknowledged data only.
	probeReady := false
	for i := 0; i < 40; i++ {
		b, e := c.request(3006, "/status", nil)
		if e == nil && bytes.Contains(b, []byte(old.Fixture)) {
			probeReady = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !probeReady {
		panic("retained probe not ready")
	}
	c.probe("/verify", old.Latest)
	c.report["latest_app_home_sql_preserved"] = true
	c.record("journal-import-verified")
	evidence, e := os.ReadFile(filepath.Join(stage, "report.json"))
	must(e)
	lease, e := session.AdmissionToken(ctx, j.ID)
	must(e)
	v := store.CubeRecoveryVerification{AdmissionToken: lease, RecoveryID: j.ID, SandboxID: sandboxID, AppID: appID, OldRuntimeID: j.Old.RuntimeID, NewRuntimeID: j.Target.RuntimeID, TemplateID: j.Target.TemplateID, ArtifactsSHA256: j.ArtifactsSHA256, ConfigFingerprint: j.ConfigFingerprint, TaskFingerprint: j.TaskFingerprint, CredentialSHA256: store.CubeRecoveryCredentialSHA(j.Target.TokenCiphertext, j.Target.TokenNonce), EvidenceSHA256: hashBytes(evidence), WorkspaceSHA256: plan.Artifacts["workspace"], HomeSHA256: plan.Artifacts["home"], HistorySHA256: plan.Artifacts["history"], ConfigRevision: 1, Authenticated: true, WorkspaceVerified: true, HomeVerified: true, HistoryVerified: true, ConfigApplied: true, ApplicationReady: true}
	must(session.Verify(ctx, v))
	must(session.Commit(ctx, j.ID, j.Old.RuntimeID, 1))
	must(session.Commit(ctx, j.ID, j.Old.RuntimeID, 1))
	must(session.Close())
	db := openFixture(ctx, database, migrations)
	defer db.Close()
	binding, e := db.GetRuntimeBinding(ctx, sandboxID)
	must(e)
	appRow, e := db.GetAppForOwner(ctx, appID, owner)
	must(e)
	task, e := db.GetTask(ctx, syntheticTask)
	must(e)
	cfg, e := db.GetAppConfig(ctx, appID, "JOURNAL_FIXTURE")
	must(e)
	done, e := db.GetCubeRecovery(ctx, j.ID)
	must(e)
	if binding.RuntimeID != remote.SandboxID || binding.Domain != "cube.app" || binding.ConfigRevision != 1 || binding.ConfigAppliedRevision != 1 || appRow.ID != appID || appRow.OwnerToken != owner || appRow.ExternalProjectID.String != "journal-fixture-project" || task.SandboxID != sandboxID || task.ResultJSON.String != taskResult || cfg.ValuePlaintext.String != old.Fixture || done.Phase != "complete" || done.Old.RuntimeID != old.Guest.SandboxID {
		panic("stable identity/config/history did not survive atomic rebind")
	}
	if _, e = db.AdmissionLookup(ctx, old.Guest.SandboxID); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		panic("old source quarantine lost")
	}
	c.report["isolated_binding_changed_atomically"] = true
	c.report["stable_owner_app_sandbox_config_task_preserved"] = true
	c.report["stable_url_identity_preserved"] = true
	c.report["actual_ui_router_tested"] = false
	c.record("journal-commit-pass")
	// Cleanup only exact recorded replacement after rechecking immutable metadata.
	c.detach()
	cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	actual, e := api.Get(cleanup, remote.SandboxID)
	must(e)
	if actual.Metadata["sandboxd_recovery_id"] != j.ID || actual.Metadata["sandboxd_id"] != sandboxID {
		panic("cleanup ownership mismatch")
	}
	must(api.ConfigureAdmission(cleanup, db, fixturePolicy()))
	must(api.Delete(cleanup, remote.SandboxID))
	_, e = api.Get(cleanup, remote.SandboxID)
	if !errors.As(e, &upstream) || upstream.StatusCode != 404 {
		panic("target deletion not independently confirmed")
	}
	inventory, e = worker(cleanup, "cubemastercli -a 127.0.0.1 list --all --wide")
	must(e)
	// Use the original unguarded provider reader: the isolated journal has
	// correctly quarantined the old ID locally after binding commit.
	sourceClient, e := cube.New(provider)
	must(e)
	_, sourceErr := sourceClient.Get(cleanup, old.Guest.SandboxID)
	afterBranch, branchErr := oldStateBranch(sourceErr, inventory, old.Guest.SandboxID)
	must(branchErr)
	if afterBranch != sourceBranch {
		panic("retained source identity/state branch changed during recovery")
	}
	tasks, e = worker(cleanup, "timeout 5s ctr --address /data/cubelet/cubelet.sock --namespace default tasks list 2>/dev/null")
	must(e)
	if !emptyCubeTasks(tasks) {
		panic("unexpected live task after owned target cleanup")
	}
	released, e := db.AdmissionLookup(cleanup, remote.SandboxID)
	must(e)
	if released.State != "deleted" || released.Charged != 0 {
		panic("fixture target retained admission charge after cleanup")
	}
	c.report["fixture_target_admission_charge_zero"] = true
	c.report["owned_target_deleted_verified404"] = true
	c.report["provider_inventory_zero"] = sourceBranch == "missing-after-worker-loss"
	c.report["original_source_retained_verified"] = sourceBranch == "unavailable-retained"
	c.report["old_provider_state_branch"] = sourceBranch
	c.record("journal-acceptance-pass")
	fmt.Println("PASS: isolated recovery journal atomically rebound stable identities and retained latest app/home/SQL; synthetic task history roundtrip passed; owned target deleted")
	return nil
}
func main() {
	if e := runJournal(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
