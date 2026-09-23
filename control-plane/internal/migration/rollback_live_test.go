package migration

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/api"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/sandboxspec"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/wake"
)

type realRollbackFixture struct{ *fixtureBackend }

func (f *realRollbackFixture) StopSource(ctx context.Context, m *store.RuntimeMigration) error {
	return f.OfflineBackend.StopSource(ctx, m)
}
func (f *realRollbackFixture) ValidateRollback(ctx context.Context, m *store.RuntimeMigration) error {
	return f.OfflineBackend.ValidateRollback(ctx, m)
}
func (f *realRollbackFixture) PrepareRollback(ctx context.Context, m *store.RuntimeMigration) error {
	return f.OfflineBackend.PrepareRollback(ctx, m)
}

// Opt-in native-host test. Only the hypervisor is replaced with a filesystem
// fixture; retained Docker rename and normal wake/recreation are real. HTTP
// readiness is checked inside the disposable container, without host routing.
func TestLiveChangedConfigRollbackNormalWake(t *testing.T) {
	if os.Getenv("CUBE_ROLLBACK_DOCKER_FIXTURE") != "1" {
		t.Skip("requires isolated native Docker fixture host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("native root fixture required")
	}
	if _, e := os.Stat("/.dockerenv"); e == nil {
		t.Fatal("must run natively")
	}
	id := ulid.Make().String()
	engine, fixture, id, _ := fixtureID(t, id)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := filepath.Dir(fixture.source)
	home := filepath.Join(root, id)
	source := filepath.Join(home, "workspace", "app")
	if e := os.MkdirAll(filepath.Dir(source), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(fixture.source, source); e != nil {
		t.Fatal(e)
	}
	fixture.source = source
	fixture.WorkspaceRoot = root
	fixture.Docker = docker.NewClient()
	// Only a test-tagged image is built; the cached base is never retagged.
	image := "cube-rollback-fixture:" + strings.ToLower(id)
	dockerfile := []byte("FROM node:22-bookworm-slim\nUSER 1000:1000\nCMD [\"node\",\"/home/sandbox/workspace/app/server.cjs\"]\n")
	if e := os.WriteFile(filepath.Join(root, "Dockerfile"), dockerfile, 0600); e != nil {
		t.Fatal(e)
	}
	command := exec.CommandContext(ctx, "docker", "build", "--network=none", "-t", image, "-f", filepath.Join(root, "Dockerfile"), root)
	if out, e := command.CombinedOutput(); e != nil {
		t.Fatalf("fixture build: %v %s", e, out)
	}
	t.Cleanup(func() { exec.Command("docker", "image", "rm", image).Run() })
	if e := os.WriteFile(filepath.Join(source, "server.cjs"), []byte(`require('http').createServer((req,res)=>res.end(process.env.PUBLIC_VALUE||'original')).listen(3000,'127.0.0.1')`), 0644); e != nil {
		t.Fatal(e)
	}
	if e := filepath.Walk(root, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		return os.Chmod(path, info.Mode()|0055)
	}); e != nil {
		t.Fatal(e)
	}
	sb, e := engine.Store.Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	sb.WorkspaceMnt = home
	sb.Image = image
	specEnv := sandboxspec.Env{Image: image, Network: "none", Userns: "host", PreviewDomain: "fixture.invalid", Limits: sandboxspec.Limits{Memory: "256m", CPUs: "1"}}
	container, e := fixture.Docker.Run(ctx, sandboxspec.Build(sb, specEnv))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		fixture.Docker.Remove(context.Background(), "s-"+id)
		fixture.Docker.Remove(context.Background(), retainedDockerName(id))
	})
	if _, e = engine.Store.DB().ExecContext(ctx, `DELETE FROM runtime_migration WHERE sandbox_id=?`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = engine.Store.DB().ExecContext(ctx, `UPDATE sandbox SET workspace_mnt=?,image=?,container_id=?,status='running' WHERE id=?`, home, image, container, id); e != nil {
		t.Fatal(e)
	}
	if e = engine.Store.BeginRuntimeMigration(ctx, id, "react-vite", "fixture-template", "fixture.invalid"); e != nil {
		t.Fatal(e)
	}
	engine.Backend = &realRollbackFixture{fixture}
	if e = engine.Run(ctx, id); e != nil {
		t.Fatal(e)
	}
	if e = engine.Store.CreateAppConfig(ctx, &store.AppConfig{ID: "changed-runtime", AppID: "durable-app", Key: "PUBLIC_VALUE", ValuePlaintext: sql.NullString{String: "changed-after-cube", Valid: true}, AccessPolicy: "runtime_access"}); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(fixture.target, "data", "owner.db"), []byte("new Cube writes"), 0600); e != nil {
		t.Fatal(e)
	}
	// Lose the rename acknowledgement after Docker has applied it, before
	// RecordRetainedDocker can commit. Recovery must find the original by ID.
	dockerPath, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("CUBE_FIXTURE_DOCKER", dockerPath)
	t.Setenv("CUBE_FIXTURE_RENAME_MARKER", filepath.Join(root, "rename-applied"))
	wrapper := filepath.Join(root, "docker-response-loss")
	if e = os.WriteFile(wrapper, []byte(`#!/bin/sh
if [ "$1" = rename ] && [ ! -e "$CUBE_FIXTURE_RENAME_MARKER" ]; then
  "$CUBE_FIXTURE_DOCKER" "$@" || exit "$?"
  : > "$CUBE_FIXTURE_RENAME_MARKER"
  exit 1
fi
exec "$CUBE_FIXTURE_DOCKER" "$@"
`), 0700); e != nil {
		t.Fatal(e)
	}
	fixture.Docker.Bin = wrapper
	t.Cleanup(func() { fixture.Docker.Bin = dockerPath })
	if e = engine.Rollback(ctx, id); e == nil {
		t.Fatal("rename response loss was not injected")
	}
	interrupted, e := engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil || interrupted.Phase != "rollback_restored" || interrupted.RetainedDockerName != "" {
		t.Fatal("rename acknowledged before response", e)
	}
	if e = engine.Rollback(ctx, id); e != nil {
		t.Fatal("rename recovery failed", e)
	}
	m, e := engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil || !m.RollbackRecreate {
		t.Fatal("recreation intent missing", e)
	}
	if _, e = fixture.Docker.Inspect(ctx, "s-"+id); !errors.Is(e, docker.ErrNotFound) {
		t.Fatal("canonical stale container remains", e)
	}
	old, e := fixture.Docker.Inspect(ctx, container)
	if e != nil || old.State.Running || strings.TrimPrefix(old.Name, "/") != retainedDockerName(id) {
		t.Fatal("original source not retained stopped", e)
	}
	// The importer in this fixture runs as root, matching the real backend's
	// normalization of the app subtree before normal unprivileged Docker wake.
	if e = filepath.Walk(source, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		return os.Lchown(path, 1000, 1000)
	}); e != nil {
		t.Fatal(e)
	}
	handler, e := wake.New(engine.Store, fixture.Docker, "fixture.invalid", wake.Config{}, wake.AdmitConfig{WakeCostMB: 1, FloorPct: 1}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal(e)
	}
	server := &api.Server{Store: engine.Store, CubeAllApps: true, PreviewDomain: "fixture.invalid"}
	preview := httptest.NewRequest("GET", "http://s-"+id+"-3000.preview.fixture.invalid/", nil)
	if server.TryServeCubePreview(httptest.NewRecorder(), preview) {
		t.Fatal("global Cube mode intercepted rolled-back Docker preview")
	}
	handler.CubePreview = server.TryServeCubePreview
	handler.Image = image
	recreated := false
	handler.Recreate = func(ctx context.Context, current *store.Sandbox) error {
		recreated = true
		entries, e := appenv.For(ctx, engine.Store, nil, current.AppID.String)
		if e != nil {
			return e
		}
		env := specEnv
		env.AppEnv = entries
		spec := sandboxspec.Build(current, env)
		if e = fixture.Docker.Remove(ctx, spec.Name); e != nil && !errors.Is(e, docker.ErrNotFound) {
			return e
		}
		_, e = fixture.Docker.Run(ctx, spec)
		return e
	}
	request := httptest.NewRequest("POST", "/wake/"+id, nil)
	request.SetPathValue("id", id)
	response := httptest.NewRecorder()
	handler.ServeJSON(response, request)
	if response.Code != 200 || !recreated {
		t.Fatalf("normal wake: %d %s recreated=%v", response.Code, response.Body.String(), recreated)
	}
	e = wait(ctx, 10*time.Second, func() bool {
		result, e := fixture.Docker.Exec(ctx, "s-"+id, []string{"node", "-e", `fetch('http://127.0.0.1:3000').then(r=>r.text()).then(t=>{if(t!=='changed-after-cube')process.exit(1)}).catch(()=>process.exit(2))`})
		return e == nil && result.ExitCode == 0
	})
	if e != nil {
		t.Fatal("recreated app not ready with current configuration", e)
	}
	saved, e := os.ReadFile(filepath.Join(source, "data", "owner.db"))
	if e != nil || string(saved) != "new Cube writes" {
		t.Fatal("post-Cube owner data was lost", e)
	}
	current, e := engine.Store.Get(ctx, id)
	if e != nil || !current.ContainerID.Valid || current.ContainerID.String == container || current.Status != "running" {
		t.Fatal("normal wake did not acknowledge new running identity", e)
	}
	t.Log("real retained Docker rename + current appenv/sandboxspec normal wake + HTTP readiness passed; Cube side is a filesystem fixture")
}
