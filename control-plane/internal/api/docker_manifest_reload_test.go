package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/loopback"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/wake"
)

type dockerReloadState struct {
	ImageID                           string
	Container                         docker.ContainerJSON
	Status                            runtime.Status
	Stops, Removes, Starts, Recreates int
}

// A subprocess Docker CLI fixture exercises the real wrapper and wake path.
func TestDockerReloadCLIHelper(t *testing.T) {
	if os.Getenv("DOCKER_RELOAD_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	statePath := os.Getenv("DOCKER_RELOAD_STATE")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		os.Exit(2)
	}
	var state dockerReloadState
	if json.Unmarshal(raw, &state) != nil {
		os.Exit(2)
	}
	switch args[0] {
	case "image":
		fmt.Fprintln(os.Stdout, state.ImageID)
		os.Exit(0)
	case "inspect":
		if state.Container.ID == "" {
			fmt.Fprintln(os.Stderr, "Error: No such object")
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(state.Container)
		os.Exit(0)
	case "stop":
		state.Stops++
		state.Container.State.Running = false
		state.Container.State.Status = "exited"
	case "rm":
		state.Removes++
		state.Container.ID = ""
	case "start":
		state.Starts++
		state.Container.State.Running = true
		state.Container.State.Status = "running"
	default:
		fmt.Fprintln(os.Stderr, "unexpected command")
		os.Exit(2)
	}
	raw, _ = json.Marshal(state)
	if os.WriteFile(statePath, raw, 0600) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type dockerReloadFixture struct {
	s                          *Server
	id, appID, root, statePath string
	mu                         sync.Mutex
	failRecreate, wrongAck     bool
	taskEntered, taskRelease   chan struct{}
	bootSource                 string
}

func (f *dockerReloadFixture) read(t *testing.T) dockerReloadState {
	t.Helper()
	raw, err := os.ReadFile(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var out dockerReloadState
	if json.Unmarshal(raw, &out) != nil {
		t.Fatal("bad fixture state")
	}
	return out
}
func (f *dockerReloadFixture) write(t *testing.T, state dockerReloadState) {
	t.Helper()
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(f.statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *dockerReloadFixture) manifest(t *testing.T, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, f.id, appSubdir, "sandbox.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}
func newDockerReloadFixture(t *testing.T) *dockerReloadFixture {
	t.Helper()
	s, appID := newConfigTestServer(t)
	root, err := os.MkdirTemp("/tmp", "dmr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	f := &dockerReloadFixture{s: s, id: "01M2QHT40D9S9W5MFNN32F1DXK", appID: appID, root: root, statePath: filepath.Join(root, "docker.json"), taskEntered: make(chan struct{}), taskRelease: make(chan struct{})}
	s.Locks = idlock.New()
	s.Image = "reviewed-image:fixture"
	s.PreviewDomain = "fixture.test"
	s.Loopback = &loopback.Manager{Root: root}
	mnt := filepath.Join(root, f.id)
	if err := os.MkdirAll(filepath.Join(mnt, appSubdir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mnt, ".runtimed"), 0700); err != nil {
		t.Fatal(err)
	}
	f.manifest(t, reloadManifestSource)
	sb := &store.Sandbox{ID: f.id, Status: "running", AppID: sql.NullString{String: appID, Valid: true}, WorkspaceMnt: mnt, WorkspaceImg: mnt, Image: s.Image, Ports: []int{3000}, ContainerID: sql.NullString{String: "fixture-0", Valid: true}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	state := dockerReloadState{ImageID: "sha256:" + strings.Repeat("a", 64)}
	state.Container.ID = "fixture-0"
	state.Container.Image = state.ImageID
	state.Container.Config.Image = s.Image
	state.Container.State.Running = true
	state.Container.State.Status = "running"
	state.Status.Runtimed.BootedAt = time.Now()
	f.write(t, state)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nGORACE=atexit_sleep_ms=0 DOCKER_RELOAD_HELPER=1 DOCKER_RELOAD_STATE='" + f.statePath + "' exec '" + exe + "' -test.run=TestDockerReloadCLIHelper -- \"$@\"\n"
	bin := filepath.Join(root, "docker")
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s.Docker = &docker.Client{Bin: bin}
	listener, err := net.Listen("unix", filepath.Join(mnt, ".runtimed", "sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tasks" {
			close(f.taskEntered)
			<-f.taskRelease
			w.WriteHeader(409)
			return
		}
		if r.URL.Path != "/status" {
			w.WriteHeader(404)
			return
		}
		raw, err := os.ReadFile(f.statePath)
		if err != nil {
			w.WriteHeader(503)
			return
		}
		var state dockerReloadState
		if json.Unmarshal(raw, &state) != nil {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(state.Status)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	s.Wake, err = wake.New(s.Store, s.Docker, s.PreviewDomain, wake.Config{}, wake.AdmitConfig{WakeCostMB: 1, FloorPct: 0.01}, nil, s.Locks, s.Log)
	if err != nil {
		t.Fatal(err)
	}
	s.Wake.Image = s.Image
	s.Wake.Recreate = func(ctx context.Context, sb *store.Sandbox) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failRecreate {
			return errors.New("synthetic recreate failure")
		}
		state := f.read(t)
		state.Recreates++
		state.Container.ID = fmt.Sprintf("fixture-%d", state.Recreates)
		state.Container.Config.Image = s.Image
		state.Container.Image = state.ImageID
		state.Container.State.Running = false
		if f.bootSource != "" {
			f.manifest(t, f.bootSource)
		}
		raw, present, err := runtime.ReadAppManifest(filepath.Join(mnt, appSubdir))
		if err != nil {
			return err
		}
		env, err := appenv.For(ctx, s.Store, s.Secrets, appID)
		if err != nil {
			return err
		}
		state.Status.ManifestSHA256 = runtime.ManifestSourceDigest(raw, present)
		state.Status.AppConfigRevision = runtime.DockerConfigRevision(env)
		state.Status.Runtimed.BootedAt = time.Now()
		if f.wrongAck {
			state.Status.ManifestSHA256 = "not-applied"
		}
		f.write(t, state)
		return nil
	}
	return f
}
func (f *dockerReloadFixture) call(body, owner string) int {
	return cubeRequest(f.s, "POST", "/v1/sandboxes/"+f.id+"/recreate", body, owner).Code
}

func TestDockerManifestReloadAcknowledgesActualBootAndConfig(t *testing.T) {
	f := newDockerReloadFixture(t)
	for i := 0; i < 2; i++ {
		if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
			t.Fatalf("reload%d HTTP%d", i, got)
		}
	}
	first := f.read(t)
	if first.Recreates != 1 || first.Stops != 1 {
		t.Fatalf("duplicate recreation %+v", first)
	}
	f.manifest(t, reloadManifestSource+"# new revision\n")
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatalf("changed HTTP%d", got)
	}
	w := do(f.s, "POST", "/v1/apps/"+f.appID+"/config", `{"key":"COLOR","value":"blue","access_policy":"runtime_access"}`, cfgTenant, map[string]string{"id": f.appID})
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	for i := 0; i < 2; i++ {
		if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
			t.Fatalf("config HTTP%d", got)
		}
	}
	if got := f.read(t).Recreates; got != 3 {
		t.Fatalf("want3 actual revisions, got%d", got)
	}
	// External replacement has no acknowledgement; an old successful request
	// must never suppress activation on this different live process.
	state := f.read(t)
	state.Container.ID = "external-container"
	state.Status.ManifestSHA256 = ""
	f.write(t, state)
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatalf("external HTTP%d", got)
	}
	if f.read(t).Recreates != 4 {
		t.Fatal("external replacement reused stale acknowledgement")
	}
	for _, body := range []string{"", `{}`, `{"reload_manifest":false}`} {
		if got := f.call(body, cfgTenant); got != 200 {
			t.Fatalf("legacy HTTP%d", got)
		}
	}
	if f.read(t).Recreates != 7 {
		t.Fatal("ordinary config recreate changed semantics")
	}
}

func TestDockerManifestReloadConcurrentRetriesAndWake(t *testing.T) {
	f := newDockerReloadFixture(t)
	var wg sync.WaitGroup
	codes := make(chan int, 5)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- f.call(`{"reload_manifest":true}`, cfgTenant) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); codes <- cubeRequest(f.s, "POST", "/wake/"+f.id, "", cfgTenant).Code }()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("wake/recreate lock deadlock")
	}
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("concurrent HTTP%d", code)
		}
	}
	if f.read(t).Recreates != 1 {
		t.Fatal("concurrent duplicate recreation")
	}
}

func TestDockerManifestReloadRejectsBeforeStopping(t *testing.T) {
	cases := []struct {
		name, body, owner, source string
		want                      int
	}{
		{"foreign", `{"reload_manifest":true}`, "other", reloadManifestSource, 404},
		{"null", `null`, cfgTenant, reloadManifestSource, 400},
		{"malformed", `{"reload_manifest":"true"}`, cfgTenant, reloadManifestSource, 400},
		{"trailing", `{} {}`, cfgTenant, reloadManifestSource, 400},
		{"invalid", `{"reload_manifest":true}`, cfgTenant, "version: 1\nservices: {}\n", 422},
		{"port", `{"reload_manifest":true}`, cfgTenant, strings.Replace(reloadManifestSource, "3000", "9000", 1), 422},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDockerReloadFixture(t)
			f.manifest(t, tc.source)
			if got := f.call(tc.body, tc.owner); got != tc.want {
				t.Fatalf("HTTP%d want%d", got, tc.want)
			}
			state := f.read(t)
			if state.Stops+state.Removes+state.Recreates != 0 || !state.Container.State.Running {
				t.Fatal("rejected input changed live container")
			}
		})
	}
}

func TestDockerManifestReloadBusyEvenWhenStoredStopped(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(fmt.Sprint(durable), func(t *testing.T) {
			f := newDockerReloadFixture(t)
			if durable {
				if err := f.s.Store.CreateTask(context.Background(), &store.Task{TaskID: "active-task", SandboxID: f.id, Agent: "claude-code", Status: "running"}); err != nil {
					t.Fatal(err)
				}
				if err := f.s.Store.MarkStoppedAt(context.Background(), f.id, time.Now()); err != nil {
					t.Fatal(err)
				}
			} else {
				state := f.read(t)
				state.Status.ActiveTask = &runtime.ActiveTask{ID: "live-task"}
				f.write(t, state)
			}
			if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 409 {
				t.Fatalf("busy HTTP%d", got)
			}
			if f.read(t).Stops != 0 {
				t.Fatal("stopped active task")
			}
		})
	}
}

func TestDockerManifestReloadMissingAndFailedRetry(t *testing.T) {
	f := newDockerReloadFixture(t)
	if err := os.Remove(filepath.Join(f.root, f.id, appSubdir, "sandbox.yaml")); err != nil {
		t.Fatal(err)
	}
	f.failRecreate = true
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got == 200 {
		t.Fatal("failed recreation acknowledged")
	}
	f.failRecreate = false
	for i := 0; i < 2; i++ {
		if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
			t.Fatalf("missing default retry HTTP%d", got)
		}
	}
	if f.read(t).Recreates != 1 {
		t.Fatal("missing-manifest repeat restarted")
	}
}

func TestDockerManifestReloadFailedAcknowledgementCanRetry(t *testing.T) {
	f := newDockerReloadFixture(t)
	f.wrongAck = true
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("POST", "/v1/sandboxes/"+f.id+"/recreate", strings.NewReader(`{"reload_manifest":true}`))
	req = req.WithContext(auth.WithActor(ctx, auth.Actor{Name: cfgTenant, Kind: "service"}))
	response := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(response, req)
	if response.Code == 200 {
		t.Fatal("unapplied manifest falsely acknowledged")
	}
	if f.read(t).Recreates != 1 {
		t.Fatal("fixture never reached failed acknowledgement")
	}
	f.wrongAck = false
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatalf("ack retry HTTP%d", got)
	}
	if f.read(t).Recreates != 2 {
		t.Fatal("failed acknowledgement suppressed retry")
	}
}

func TestDockerManifestReloadMutableImageTag(t *testing.T) {
	f := newDockerReloadFixture(t)
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatal(got)
	}
	state := f.read(t)
	state.ImageID = "sha256:" + strings.Repeat("b", 64)
	f.write(t, state)
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatal(got)
	}
	state = f.read(t)
	if state.Recreates != 2 || state.Container.Image != state.ImageID {
		t.Fatal("same tag hid changed image")
	}
}
func TestDockerManifestReloadAcknowledgesOnlyValidatedBoot(t *testing.T) {
	f := newDockerReloadFixture(t)
	f.bootSource = reloadManifestSource + "# changed while recreating\n"
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("POST", "/v1/sandboxes/"+f.id+"/recreate", strings.NewReader(`{"reload_manifest":true}`))
	req = req.WithContext(auth.WithActor(ctx, auth.Actor{Name: cfgTenant, Kind: "service"}))
	response := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(response, req)
	if response.Code == 200 || f.read(t).Recreates != 1 {
		t.Fatalf("changed boot falsely acknowledged: %d", response.Code)
	}
	f.bootSource = ""
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatal(got)
	}
	if f.read(t).Recreates != 1 {
		t.Fatal("retry failed to recognize actual newly validated boot")
	}
}

func TestDockerManifestReloadSerializesTaskSubmission(t *testing.T) {
	f := newDockerReloadFixture(t)
	taskDone := make(chan int, 1)
	go func() {
		taskDone <- cubeRequest(f.s, "POST", "/v1/sandboxes/"+f.id+"/tasks", `{"prompt":"synthetic no-agent task","agent":"opencode"}`, cfgTenant).Code
	}()
	select {
	case <-f.taskEntered:
	case code := <-taskDone:
		t.Fatalf("task never reached runtime: %d", code)
	case <-time.After(3 * time.Second):
		t.Fatal("task submission blocked")
	}
	reloadDone := make(chan int, 1)
	go func() { reloadDone <- f.call(`{"reload_manifest":true}`, cfgTenant) }()
	select {
	case code := <-reloadDone:
		t.Fatalf("reload escaped task lock: %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	if err := f.s.Store.CreateTask(context.Background(), &store.Task{TaskID: "concurrent-live-task", SandboxID: f.id, Agent: "opencode", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	close(f.taskRelease)
	select {
	case code := <-taskDone:
		if code != 409 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task stuck")
	}
	select {
	case code := <-reloadDone:
		if code != 409 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reload stuck")
	}
	if f.read(t).Stops != 0 {
		t.Fatal("reload stopped newly submitted task")
	}
}
func TestDockerTaskSubmissionStoppedWakeLockHandoff(t *testing.T) {
	f := newDockerReloadFixture(t)
	state := f.read(t)
	state.Container.State.Running = false
	f.write(t, state)
	if err := f.s.Store.MarkStoppedAt(context.Background(), f.id, time.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- cubeRequest(f.s, "POST", "/v1/sandboxes/"+f.id+"/tasks", `{}`, cfgTenant).Code }()
	select {
	case code := <-done:
		if code != 400 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task wake lock deadlock")
	}
	if f.read(t).Starts != 1 {
		t.Fatal("stopped sandbox wasn't woken before request validation")
	}
}

func TestDockerManifestReloadDoesNotReuseExternalContainerAcknowledgement(t *testing.T) {
	f := newDockerReloadFixture(t)
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatal(got)
	}
	state := f.read(t)
	state.Container.ID = "unregistered-external-container"
	f.write(t, state)
	if got := f.call(`{"reload_manifest":true}`, cfgTenant); got != 200 {
		t.Fatal(got)
	}
	if f.read(t).Recreates != 2 {
		t.Fatal("external replacement reused prior acknowledgement")
	}
}
