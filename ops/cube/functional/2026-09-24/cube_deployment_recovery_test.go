package migration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// This test executes real Docker and Cube lifecycle calls. It is opt-in, only
// runs in the marked disposable benchmark VM, and never uses production DBs.
// No agent/model execution: transport history is a synthetic canonical source artifact.
func TestOperatorIndependentDeploymentRecovery(t *testing.T) {
	if os.Getenv("CUBE_DEPLOYMENT_RECOVERY_LIVE") != "1" {
		t.Skip("requires marked isolated VM and explicit live opt-in")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires native VM root")
	}
	if _, err := os.Stat("/root/bench-ready"); err != nil {
		t.Fatal("not the isolated benchmark VM")
	}
	if _, err := os.Stat("/root/cube-pilot"); err != nil {
		t.Fatal("pilot fixture directory missing")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Fatal("run the compiled test natively, not inside Docker")
	}
	image := os.Getenv("CUBE_MIGRATION_IMAGE")
	template := os.Getenv("CUBE_MIGRATION_TEMPLATE")
	if image == "" || template == "" {
		t.Fatal("CUBE_MIGRATION_IMAGE and CUBE_MIGRATION_TEMPLATE are required")
	}
	raw, err := os.ReadFile("/root/cube-pilot/test-secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	if err = json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	client, err := cube.New(cube.Config{APIURL: "http://127.0.0.1:3000", APIKey: keys["cube_key"]})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("/data", "cube-recovery-original-")
	if err != nil {
		t.Fatal(err)
	}
	id, appID, oldTask := ulid.Make().String(), ulid.Make().String(), ulid.Make().String()
	workspaceRoot := filepath.Join(root, "workspaces")
	home := filepath.Join(workspaceRoot, id)
	appRoot := filepath.Join(home, "workspace", "app")
	tasksRoot := filepath.Join(home, ".runtimed", "tasks")
	for _, directory := range []string{appRoot, filepath.Join(tasksRoot, oldTask), filepath.Join(home, "owner-tools"), filepath.Join(home, ".claude"), filepath.Join(root, "library")} {
		if err = os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(appRoot, "server.js"), `require('http').createServer((req,res)=>{res.setHeader('Content-Type','text/html');res.end('<h1>Migration fixture</h1>')}).listen(3000,'0.0.0.0')`)
	write(filepath.Join(appRoot, "sandbox.yaml"), "version: 1\nweb:\n  command: node server.js\n  port: 3000\n  health_path: /\nbuild:\n  command: \"\"\n")
	write(filepath.Join(appRoot, ".env"), "OWNER_SECRET=private-fixture\n")
	write(filepath.Join(home, "owner-tools", "settings"), "private owner settings")
	write(filepath.Join(home, ".claude", "auth.json"), "synthetic retained provider identity")
	write(filepath.Join(root, "deployment.json"), `{"fixture":true,"image":"`+image+`","template":"`+template+`"}`)
	write(filepath.Join(root, "home-manifest.json"), `{"scope":"full independent owner-home backup including retained provider state"}`)
	git := func(arguments ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, "git", append([]string{"-C", appRoot}, arguments...)...)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.name", "Migration fixture")
	git("config", "user.email", "fixture@example.invalid")
	git("add", ".")
	git("commit", "-m", "private fixture")
	checkpoint := git("rev-parse", "HEAD")
	git("update-ref", "refs/sandboxd/checkpoints/"+oldTask, checkpoint)
	oldResult := runtime.TaskResult{ID: oldTask, Status: runtime.TaskSucceeded, CheckpointID: checkpoint}
	resultJSON, _ := json.Marshal(oldResult)
	write(filepath.Join(tasksRoot, oldTask, "result.json"), string(resultJSON))
	write(filepath.Join(tasksRoot, oldTask, "events.jsonl"), "{\"id\":0,\"type\":\"done\",\"data\":{\"fixture\":\"source\"}}\n")
	if err = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, 1000, 1000)
	}); err != nil {
		t.Fatal(err)
	}
	targetDeleted := false
	containerRemoved := false
	containerName := "cube-migration-fixture-" + strings.ToLower(id)
	command := exec.CommandContext(ctx, "docker", "run", "--detach", "--name", containerName, "--user", "1000:1000", "--network", "none", "--cpus", "1", "--memory", "1g", "--label", "sandboxd.managed=true", "--volume", home+":/home/sandbox", "--entrypoint", "node", image, "/home/sandbox/workspace/app/server.js")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("disposable source: %v %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))
	db, err := store.Open(ctx, "file:"+filepath.Join(root, "sandboxd.db")+"?_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.Load("", filepath.Join(root, "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewMigrationBroker(ctx, egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	backend := &OfflineBackend{Broker: broker, Store: db, Docker: docker.NewClient(), Cube: client, Secrets: cipher, ProxyURL: "http://127.0.0.1:80", ArchiveDir: filepath.Join(root, "archives"), WorkspaceRoot: workspaceRoot}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if db == nil {
			t.Error("fixture store unavailable during cleanup; retained owned paths require review")
			return
		}
		m, e := db.GetRuntimeMigration(cleanup, id)
		if e != nil && !errors.Is(e, store.ErrNotFound) {
			t.Errorf("fixture migration lookup during cleanup: %v", e)
		} else if e == nil && m.Binding.RuntimeID != "" && !targetDeleted {
			if e = backend.DeleteTarget(cleanup, m); e != nil {
				t.Errorf("fixture Cube cleanup: %v", e)
			}
		}
		if e := func() error {
			if containerRemoved {
				return nil
			}
			return backend.Docker.Remove(cleanup, containerID)
		}(); e != nil {
			t.Errorf("fixture Docker cleanup: %v", e)
		}
		db.Close()
		if !t.Failed() {
			os.RemoveAll(root)
		}
	})
	if err = db.CreateApp(ctx, &store.App{ID: appID, Name: "disposable migration", OwnerToken: "migration-fixture-owner"}); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(ctx, &store.Sandbox{ID: id, Status: "running", AppID: sql.NullString{String: appID, Valid: true}, Image: image, WorkspaceMnt: home, WorkspaceImg: home, Ports: []int{3000}, WebPort: sql.NullInt64{Int64: 3000, Valid: true}, Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if err = db.MarkRunning(ctx, id, containerID, ""); err != nil {
		t.Fatal(err)
	}
	if err = db.CreateTask(ctx, &store.Task{TaskID: oldTask, SandboxID: id, Agent: "fixture", Prompt: "deterministic transport artifact"}); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishTask(ctx, oldTask, "succeeded", string(resultJSON)); err != nil {
		t.Fatal(err)
	}
	configID, snapshotID := ulid.Make().String(), ulid.Make().String()
	configCipher, configNonce, err := cipher.Seal([]byte("private configuration from backup"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.CreateAppConfig(ctx, &store.AppConfig{ID: configID, AppID: appID, Key: "RECOVERY_PRIVATE", Sensitive: true, ValueCiphertext: configCipher, ValueNonce: configNonce, AccessPolicy: "runtime_access"}); err != nil {
		t.Fatal(err)
	}
	var published bytes.Buffer
	zw := zip.NewWriter(&published)
	entry, err := zw.Create("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write([]byte("published source fixture")); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	libraryPath := filepath.Join(root, "library", snapshotID+".zip")
	if err = os.WriteFile(libraryPath, published.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err = db.CreateSnapshot(ctx, &store.Snapshot{ID: snapshotID, Name: "Independent backup library", OwnerToken: "migration-fixture-owner", SourceSandboxID: sql.NullString{String: id, Valid: true}, SourceAppID: sql.NullString{String: appID, Valid: true}, BaseImage: "cube-preset:node-express", Visibility: "private", Format: "cube-source-v1", Status: "ready", ImagePath: libraryPath}); err != nil {
		t.Fatal(err)
	}
	if err = db.BeginRuntimeMigration(ctx, id, "node-express", template, "cube.app"); err != nil {
		t.Fatal(err)
	}
	interrupted := false
	engine := &Engine{Store: db, Backend: backend, AfterPhase: func(phase string) error {
		if phase == "verified" && !interrupted {
			interrupted = true
			return errors.New("fixture verified-phase interruption")
		}
		return nil
	}}
	started := time.Now()
	if err = engine.Run(ctx, id); err == nil || !interrupted {
		t.Fatal("verified-phase interruption was not recorded", err)
	}
	if err = engine.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	migrationMS := time.Since(started).Milliseconds()
	m, err := db.GetRuntimeMigration(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := backend.connect(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if err = guest.RevertTask(ctx, oldTask); err != nil {
		t.Fatalf("migrated checkpoint cannot be reverted: %v", err)
	}
	if _, err = guest.PutFile(ctx, "after-cutover.txt", strings.NewReader("new Cube data survives rollback")); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if err = engine.Rollback(ctx, id); err != nil {
		t.Fatal(err)
	}
	rollbackMS := time.Since(started).Milliseconds()
	data, err := os.ReadFile(filepath.Join(appRoot, "after-cutover.txt"))
	if err != nil || string(data) != "new Cube data survives rollback" {
		t.Fatalf("target writes lost: %v", err)
	}
	for _, taskID := range []string{oldTask} {
		if _, err = runtime.ExportPrivateTaskHistory(ctx, tasksRoot, []string{taskID}); err != nil {
			t.Fatalf("rollback task history lost: %v", err)
		}
	}
	sb, err := db.Get(ctx, id)
	if err != nil || sb.RuntimeProvider != "docker" || sb.AppID.String != appID || sb.Ports[0] != 3000 {
		t.Fatal("stable provider identity lost", err)
	}
	if err = backend.Docker.Start(ctx, containerID); err != nil {
		t.Fatal(err)
	}
	if err = wait(ctx, 10*time.Second, func() bool {
		probe := exec.CommandContext(ctx, "docker", "exec", containerID, "node", "-e", `require('http').get('http://127.0.0.1:3000',r=>process.exit(r.statusCode===200?0:1)).on('error',()=>process.exit(1))`)
		return probe.Run() == nil
	}); err != nil {
		t.Fatal("restored Docker app unavailable", err)
	}
	if err = backend.Docker.Stop(ctx, containerID, 10); err != nil {
		t.Fatal(err)
	}

	// Restore a full synthetic deployment from copied files after all original
	// files and both guest instances are gone. This is independent file recovery
	// on the same physical VM; it is not an off-host disaster-recovery claim.
	m, err = db.GetRuntimeMigration(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = backend.DeleteTarget(ctx, m); err != nil {
		t.Fatal(err)
	}
	targetDeleted = true
	if err = backend.Docker.Remove(ctx, containerID); err != nil {
		t.Fatal(err)
	}
	containerRemoved = true
	backupRoot, err := os.MkdirTemp("/data", "cube-recovery-backup-")
	if err != nil {
		t.Fatal(err)
	}
	restoredRoot, err := os.MkdirTemp("/data", "cube-recovery-restored-")
	if err != nil {
		t.Fatal(err)
	}
	originalRoot := root
	// Ensure the hot SQLite snapshot includes committed WAL state. Every other
	// scope is quiesced and copied using the fsyncing independent-inode helper.
	if _, err = db.DB().ExecContext(ctx, `VACUUM INTO ?`, filepath.Join(backupRoot, "sandboxd.db")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"workspaces", "archives", "library", "secrets.key", "deployment.json", "home-manifest.json"} {
		copyFixtureBackup(t, filepath.Join(originalRoot, name), filepath.Join(backupRoot, name))
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = nil
	if err = os.RemoveAll(originalRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(originalRoot); !os.IsNotExist(err) {
		t.Fatal("original deployment still exists")
	}
	for _, name := range []string{"sandboxd.db", "workspaces", "archives", "library", "secrets.key", "deployment.json", "home-manifest.json"} {
		copyFixtureBackup(t, filepath.Join(backupRoot, name), filepath.Join(restoredRoot, name))
	}
	root = restoredRoot
	home = filepath.Join(root, "workspaces", id)
	appRoot = filepath.Join(home, "workspace", "app")
	tasksRoot = filepath.Join(home, ".runtimed", "tasks")
	db, err = store.Open(ctx, "file:"+filepath.Join(root, "sandboxd.db")+"?_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	backend.Store = db
	backend.WorkspaceRoot = filepath.Join(root, "workspaces")
	backend.ArchiveDir = filepath.Join(root, "archives")
	var integrity string
	if err = db.DB().QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("restored SQLite integrity", err)
	}
	restoredCipher, err := secrets.Load("", filepath.Join(root, "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.ListAppConfig(ctx, appID)
	if err != nil || len(config) != 1 {
		t.Fatal("restored app config", err)
	}
	configPlain, err := restoredCipher.Open(config[0].ValueCiphertext, config[0].ValueNonce)
	if err != nil || string(configPlain) != "private configuration from backup" {
		t.Fatal("restored key cannot decrypt app configuration", err)
	}
	m, err = db.GetRuntimeMigration(ctx, id)
	if err != nil || m.Phase != "rolled_back" {
		t.Fatal("restored migration journal", err)
	}
	if _, err = restoredCipher.Open(m.Binding.TokenCiphertext, m.Binding.TokenNonce); err != nil {
		t.Fatal("restored key cannot decrypt runtime binding", err)
	}
	// Explicit operator relocation of only this fixture's active paths. Historical
	// journal source identity remains historical; it is not forged into new evidence.
	if _, err = db.DB().ExecContext(ctx, `UPDATE sandbox SET workspace_img=?,workspace_mnt=? WHERE id=?`, home, home, id); err != nil {
		t.Fatal(err)
	}
	newLibraryPath := filepath.Join(root, "library", snapshotID+".zip")
	if _, err = db.DB().ExecContext(ctx, `UPDATE snapshot SET image_path=? WHERE id=?`, newLibraryPath, snapshotID); err != nil {
		t.Fatal(err)
	}
	snap, err := db.GetSnapshot(ctx, snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	gotArchive, err := os.ReadFile(snap.ImagePath)
	if err != nil || sha256.Sum256(gotArchive) != sha256.Sum256(published.Bytes()) {
		t.Fatal("restored published library differs", err)
	}
	if _, err = runtime.SanitizeSourceArchive(gotArchive); err != nil {
		t.Fatal("restored source library invalid", err)
	}
	for p, want := range map[string]string{
		filepath.Join(appRoot, "after-cutover.txt"):    "new Cube data survives rollback",
		filepath.Join(appRoot, ".env"):                 "OWNER_SECRET=private-fixture\n",
		filepath.Join(home, "owner-tools", "settings"): "private owner settings",
		filepath.Join(home, ".claude", "auth.json"):    "synthetic retained provider identity",
	} {
		got, e := os.ReadFile(p)
		if e != nil || string(got) != want {
			t.Fatal("restored private scope differs", e)
		}
	}
	if _, err = runtime.ExportPrivateTaskHistory(ctx, tasksRoot, []string{oldTask}); err != nil {
		t.Fatal("restored history", err)
	}
	if err = filepath.Walk(home, func(p string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		return os.Lchown(p, 1000, 1000)
	}); err != nil {
		t.Fatal(err)
	}
	// This extra synthetic server exposes no credentials: its private checks
	// happen inside the restored container; HTTP returns only pass/fail.
	verifyScript := `const fs=require('fs');require('http').createServer((q,r)=>{const ok=fs.readFileSync('/home/sandbox/workspace/app/after-cutover.txt','utf8')==='new Cube data survives rollback'&&fs.readFileSync('/home/sandbox/workspace/app/.env','utf8')==='OWNER_SECRET=private-fixture\n'&&fs.readFileSync('/home/sandbox/owner-tools/settings','utf8')==='private owner settings'&&process.env.RECOVERY_PRIVATE==='private configuration from backup';r.statusCode=ok?200:500;r.end(ok?'restored':'failed')}).listen(3000,'0.0.0.0')`
	command = exec.CommandContext(ctx, "docker", "run", "--detach", "--name", containerName+"-restored", "--user", "1000:1000", "--network", "none", "--cpus", "1", "--memory", "1g", "--label", "sandboxd.managed=true", "--volume", home+":/home/sandbox", "--env", "RECOVERY_PRIVATE="+string(configPlain), "--entrypoint", "node", image, "-e", verifyScript)
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatal("restored Docker recreation failed", err)
	}
	containerID = strings.TrimSpace(string(output))
	containerRemoved = false
	if err = db.MarkRunning(ctx, id, containerID, ""); err != nil {
		t.Fatal(err)
	}
	if err = wait(ctx, 15*time.Second, func() bool {
		return exec.CommandContext(ctx, "docker", "exec", containerID, "node", "-e", `require('http').get('http://127.0.0.1:3000',r=>{let b='';r.on('data',c=>b+=c);r.on('end',()=>process.exit(r.statusCode===200&&b==='restored'?0:1))}).on('error',()=>process.exit(1))`).Run() == nil
	}); err != nil {
		t.Fatal("independent restored HTTP/private/config checks failed", err)
	}
	sb, err = db.Get(ctx, id)
	if err != nil || sb.AppID.String != appID || sb.RuntimeProvider != "docker" || sb.Visibility != "private" || sb.Ports[0] != 3000 {
		t.Fatal("restored stable app/sandbox/visibility/port", err)
	}
	if err = backend.Docker.Stop(ctx, containerID, 2); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(backupRoot); err != nil {
		t.Fatal(err)
	}
	t.Logf("independent deployment recovered: consistent SQLite, key/config, full owner home, library archive, journal/task history, new Cube writes, original scopes removed, separate restored root and real Docker HTTP200; migrate=%dms rollback=%dms", migrationMS, rollbackMS)
	fmt.Fprintln(os.Stdout, "LIVE_INDEPENDENT_DEPLOYMENT_RECOVERY_PASS")
}
