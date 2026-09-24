package migration

import (
	"context"
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
func TestOperatorReviewedTemplateJournalRoundtrip(t *testing.T) {
	if os.Getenv("CUBE_REVIEWED_MIGRATION_LIVE") != "1" {
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
	root, err := os.MkdirTemp("/data", "cube-migration-roundtrip-")
	if err != nil {
		t.Fatal(err)
	}
	id, appID, oldTask := ulid.Make().String(), ulid.Make().String(), ulid.Make().String()
	workspaceRoot := filepath.Join(root, "workspaces")
	home := filepath.Join(workspaceRoot, id)
	appRoot := filepath.Join(home, "workspace", "app")
	tasksRoot := filepath.Join(home, ".runtimed", "tasks")
	for _, directory := range []string{appRoot, filepath.Join(tasksRoot, oldTask)} {
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
	containerName := "cube-migration-fixture-" + strings.ToLower(id)
	command := exec.CommandContext(ctx, "docker", "run", "--detach", "--name", containerName, "--user", "1000:1000", "--network", "none", "--label", "sandboxd.managed=true", "--volume", home+":/home/sandbox", "--entrypoint", "node", image, "/home/sandbox/workspace/app/server.js")
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
		if m, e := db.GetRuntimeMigration(cleanup, id); e == nil && m.Binding.RuntimeID != "" {
			if e = backend.DeleteTarget(cleanup, m); e != nil {
				t.Errorf("fixture Cube cleanup: %v", e)
			}
		}
		if e := backend.Docker.Remove(cleanup, containerID); e != nil {
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
	probe := exec.CommandContext(ctx, "docker", "exec", containerID, "node", "-e", `require('http').get('http://127.0.0.1:3000',r=>{let b='';r.on('data',v=>b+=v);r.on('end',()=>process.exit(b.includes('Migration fixture')?0:1))}).on('error',()=>process.exit(1))`)
	if err = wait(ctx, 10*time.Second, func() bool {
		probe = exec.CommandContext(ctx, "docker", "exec", containerID, "node", "-e", `require('http').get('http://127.0.0.1:3000',r=>process.exit(r.statusCode===200?0:1)).on('error',()=>process.exit(1))`)
		return probe.Run() == nil
	}); err != nil {
		t.Fatal("restored Docker app unavailable", err)
	}
	if err = backend.Docker.Stop(ctx, containerID, 10); err != nil {
		t.Fatal(err)
	}
	t.Logf("real Docker→Cube→write→Docker passed: migrate=%dms rollback=%dms; verified-phase interruption recovery, checkpoint/history preservation; no model call", migrationMS, rollbackMS)
	fmt.Fprintln(os.Stdout, "LIVE_MIGRATION_ROUNDTRIP_PASS")
}
