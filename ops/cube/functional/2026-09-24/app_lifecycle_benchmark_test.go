package api

// Copy this operator-only fixture into internal/api in a disposable source
// checkout, compile there, and run against the marked benchmark host. It does
// not belong in ordinary unit tests and never reads a production state store.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/loopback"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/sandboxspec"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/wake"
)

func TestOperatorMatchedAppLifecycle(t *testing.T) {
	if os.Getenv("APP_LIFECYCLE_BENCH") != "1" {
		t.Skip("explicit disposable application benchmark only")
	}
	backend := os.Getenv("APP_BENCH_BACKEND")
	if backend != "docker" && backend != "cube" {
		t.Fatal("choose docker or cube")
	}
	stage := os.Getenv("APP_BENCH_STAGE")
	if !filepath.IsAbs(stage) || !strings.Contains(stage, "app-lifecycle") {
		t.Fatal("explicit app-lifecycle staging directory required")
	}
	if _, err := os.Stat(filepath.Join(stage, "disposable-benchmark")); err != nil {
		t.Fatal("disposable marker missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// Keep the host-side runtime Unix socket below sockaddr_un path limits.
	work, err := os.MkdirTemp("/tmp", "app-lifecycle-owned-")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(work, "state.db")+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.Load("", filepath.Join(work, "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{Store: st, Secrets: cipher, Log: log, Locks: idlock.New(), LibraryRoot: filepath.Join(work, "library"), PreviewDomain: "lifecycle.invalid", PreviewURLScheme: "http", Image: "sha256:9ec445151344a7baa295b085bebe1a0352ab930601fe0d9ed2bf1283bbaa6678"}
	signing := make([]byte, 32)
	if _, err = rand.Read(signing); err != nil {
		t.Fatal(err)
	}
	s.Auth = auth.NewMiddleware(&auth.Config{PreviewSecrets: map[string]string{"fixture": hex.EncodeToString(signing)}}, nil, nil, log)
	s.Docker = docker.NewClient()
	s.Loopback = loopback.New()
	s.Loopback.Root = filepath.Join(work, "workspaces")
	s.Loopback.SeedImage = s.Image
	s.Loopback.Log = log
	s.Userns = "host"
	s.Limits = sandboxspec.Limits{CPUs: "1", Memory: "1g"}
	owned := map[string]bool{}
	report := map[string]any{"backend": backend, "started_at": time.Now().UTC().Format(time.RFC3339Nano), "runtime_preset": "react-pro", "cpu_per_app": 1, "memory_mib_per_app": 1024, "production_mutation": false, "model_calls": false, "source_commit": os.Getenv("APP_BENCH_SOURCE_COMMIT"), "samples": []map[string]any{}, "scope": "control-plane handlers plus real HTML and transformed assets; Docker host bridge vs nested Cube preview proxy; not browser rendering"}
	cleanup := func(id string) bool {
		if !owned[id] {
			return true
		}
		s.stopCubeEgress(id)
		result := cubeRequest(s, "DELETE", "/v1/sandboxes/"+id, "", cfgTenant)
		if result.Code != 204 {
			return false
		}
		delete(owned, id)
		return true
	}
	t.Cleanup(func() {
		// This store is fixture-only: include partially created resources too.
		if rows, e := st.List(context.Background()); e == nil {
			for _, row := range rows {
				owned[row.ID] = true
			}
		}
		all := true
		for id := range owned {
			if !cleanup(id) {
				all = false
			}
		}
		report["owned_sandboxes_deleted"] = all
		report["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		encoded, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile(filepath.Join(stage, "report-"+backend+".json"), encoded, 0600)
		st.Close()
		if all {
			_ = os.RemoveAll(work)
		} else {
			t.Error("owned fixture cleanup failed; retained recovery directory")
		}
	})
	if backend == "cube" {
		var key struct {
			Key string `json:"cube_key"`
		}
		data, e := os.ReadFile("/root/cube-pilot/test-secrets.json")
		if e != nil || json.Unmarshal(data, &key) != nil || key.Key == "" {
			t.Fatal("private Cube fixture credential unavailable")
		}
		s.Cube, err = cube.New(cube.Config{APIURL: "http://127.0.0.1:3000", APIKey: key.Key})
		if err != nil {
			t.Fatal(err)
		}
		s.CubeProxyURL = "http://127.0.0.1:80"
		s.CubeDomain = "cube.app"
		s.CubeAgentRelayOrigin = "https://functional.invalid"
		s.AgentProxyURL = "http://127.0.0.1:1"
		s.CubeTemplates = map[string]string{"react-pro": "tpl-ce9efc43b71248d9a0adfb90"}
		s.CubeApps = map[string]bool{}
		report["template"] = "tpl-ce9efc43b71248d9a0adfb90"
		report["image"] = "sha256:6bad30fa19584d85f0dafc1680bca5851f1bb05f55a6abe0d6002165227253d0"
		if err = s.ConfigureCubeEgress(ctx, CubeEgressConfig{Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}, BridgeURL: "https://functional.invalid/api/bridge"}); err != nil {
			t.Fatal(err)
		}
	} else {
		s.Network = os.Getenv("APP_BENCH_NETWORK")
		if !strings.HasPrefix(s.Network, "cube-app-bench-") {
			t.Fatal("dedicated internal fixture network required")
		}
		check, e := exec.CommandContext(ctx, "docker", "network", "inspect", s.Network, "--format", "{{.Internal}}").Output()
		if e != nil || strings.TrimSpace(string(check)) != "true" {
			t.Fatal("fixture network is not internal")
		}
		// The production seed command is unchanged, but this trusted one-shot copy
		// container also has no networking and shares the stated resource bounds.
		wrapper := filepath.Join(work, "seed-docker")
		if err = os.WriteFile(wrapper, []byte("#!/bin/sh\nif [ \"$1\" = run ] && [ \"$2\" = --rm ]; then shift; exec /usr/bin/docker run --network none --cpus 1 --memory 1g \"$@\"; fi\nexec /usr/bin/docker \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		s.Loopback.DockerBin = wrapper
		s.Wake, err = wake.New(st, s.Docker, s.PreviewDomain, wake.Config{TCPReadyTimeout: 30 * time.Second}, wake.AdmitConfig{}, nil, s.Locks, log)
		if err != nil {
			t.Fatal(err)
		}
		report["image"] = s.Image
		report["docker_network"] = "owned internal bridge; no external egress"
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(auth.WithActor(ctx, auth.Actor{Name: cfgTenant, Kind: "service"}))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	desiredAppMarker := ""
	ready := func(id string, sample map[string]any, phase string) bool {
		// Preserve bounded failure metadata, never response bodies, capabilities or URLs.
		lastFailure := func(path, kind string, status, size int) {
			sample[phase+"_last_failure"] = map[string]any{"path": path, "kind": kind, "status": status, "bytes": size}
		}
		// Include real app HTML, transformed entrypoint, and Vite runtime assets.
		paths := []string{"/", "/src/main.tsx", "/@vite/client"}
		if desiredAppMarker != "" {
			paths = append(paths, "/src/App.tsx")
		}
		var base, token string
		if backend == "docker" {
			detail, e := s.Docker.Inspect(ctx, "s-"+id)
			if e != nil {
				lastFailure("/", "container_inspect_failed", 0, 0)
				return false
			}
			base = "http://" + detail.BridgeIP() + ":3000"
		} else {
			access := request("POST", "/v1/sandboxes/"+id+"/preview-access", "")
			var parsed struct {
				Token string `json:"token"`
			}
			if access.Code != 200 || json.Unmarshal(access.Body.Bytes(), &parsed) != nil {
				lastFailure("/preview-access", "preview_access_failed", access.Code, 0)
				return false
			}
			token = parsed.Token
			base = s.previewURL(id, 3000)
		}
		client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		until := time.Now().Add(90 * time.Second)
		for time.Now().Before(until) && ctx.Err() == nil {
			ok := true
			checks := []map[string]any{}
			for _, path := range paths {
				var code int
				var body []byte
				if backend == "cube" {
					probe, cancelProbe := context.WithTimeout(ctx, 3*time.Second)
					r := httptest.NewRequest("GET", base+path, nil).WithContext(probe)
					r.AddCookie(&http.Cookie{Name: "sandbox_preview", Value: token})
					r.Header.Set("Sec-Fetch-Dest", "iframe")
					w := httptest.NewRecorder()
					handled := s.TryServeCubePreview(w, r)
					cancelProbe()
					if !handled {
						lastFailure(path, "preview_not_handled", 0, 0)
						ok = false
						break
					}
					code = w.Code
					body = w.Body.Bytes()
				} else {
					r, _ := http.NewRequestWithContext(ctx, "GET", base+path, nil)
					resp, e := client.Do(r)
					if e != nil {
						kind := "transport_error"
						if errors.Is(e, context.DeadlineExceeded) {
							kind = "request_timeout"
						}
						if errors.Is(e, context.Canceled) {
							kind = "request_canceled"
						}
						lastFailure(path, kind, 0, 0)
						ok = false
						break
					}
					code = resp.StatusCode
					body, _ = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
					resp.Body.Close()
				}
				valid := code == 200 && len(body) > 40
				if path == "/" {
					valid = valid && bytesContainsAny(body, []string{"/src/main.tsx", "/@vite/client"})
				} else if path == "/src/main.tsx" {
					valid = valid && bytesContainsAny(body, []string{"createRoot", "ReactDOM"})
				} else if path == "/src/App.tsx" {
					valid = valid && strings.Contains(string(body), desiredAppMarker)
				} else {
					valid = valid && strings.Contains(string(body), "WebSocket")
				}
				checks = append(checks, map[string]any{"path": path, "status": code, "bytes": len(body), "validated": valid})
				if !valid {
					kind := "content_validation_failed"
					if code != 200 {
						kind = "unexpected_http_status"
					}
					lastFailure(path, kind, code, len(body))
					ok = false
					break
				}
			}
			if ok {
				sample[phase+"_http_checks"] = checks
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		sample[phase+"_failed"] = true
		return false
	}
	iterations := 4
	editProbe := os.Getenv("APP_BENCH_EDIT_PROBE") == "1"
	if editProbe {
		iterations = 1
		report["edit_probe"] = true
	}
	report["remix_explicitly_skipped"] = os.Getenv("APP_BENCH_SKIP_REMIX") == "1"
	for iteration := 0; iteration < iterations; iteration++ {
		sample := map[string]any{"iteration": iteration, "warmup": iteration == 0, "started_at": time.Now().UTC().Format(time.RFC3339Nano)}
		report["samples"] = append(report["samples"].([]map[string]any), sample)
		started := time.Now()
		createdApp := request("POST", "/v1/apps", `{"name":"Disposable React Pro lifecycle","runtime_preset":"react-pro","external_user_id":"synthetic-owner"}`)
		var app v1App
		if createdApp.Code != 201 || json.Unmarshal(createdApp.Body.Bytes(), &app) != nil {
			sample["failure"] = "app create"
			t.Fatal("app create failed")
		}
		if backend == "cube" {
			s.CubeApps[app.ID] = true
		}
		created := request("POST", "/v1/apps/"+app.ID+"/sandbox", `{"runtime_preset":"react-pro"}`)
		var sb sandboxResp
		if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &sb) != nil || sb.ID == "" {
			sample["failure"] = fmt.Sprintf("sandbox create HTTP%d", created.Code)
			t.Fatal("sandbox create failed")
		}
		owned[sb.ID] = true
		sample["create_api_ms"] = time.Since(started).Milliseconds()
		if !ready(sb.ID, sample, "create") {
			t.Fatal("create HTTP/asset readiness failed")
		}
		sample["create_ready_ms"] = time.Since(started).Milliseconds()
		if editProbe {
			original := request("GET", "/v1/sandboxes/"+sb.ID+"/files/content?path=src/App.tsx", "")
			if original.Code != 200 {
				t.Fatal("read App.tsx failed")
			}
			desiredAppMarker = "LIFECYCLE_EDIT_MARKER"
			edited := original.Body.String() + "\nexport const LIFECYCLE_EDIT_MARKER = import.meta.env.VITE_LIFECYCLE_FIXTURE;\n"
			editAt := time.Now()
			if request("PUT", "/v1/sandboxes/"+sb.ID+"/files?path=src/App.tsx", edited).Code != 200 {
				t.Fatal("App.tsx edit failed")
			}
			if !ready(sb.ID, sample, "source_edit") {
				t.Fatal("transformed source edit not ready")
			}
			sample["source_edit_ready_ms"] = time.Since(editAt).Milliseconds()
			envAt := time.Now()
			desiredAppMarker = "env-fixture-value"
			if request("PUT", "/v1/sandboxes/"+sb.ID+"/files?path=.env.local", "VITE_LIFECYCLE_FIXTURE=env-fixture-value\n").Code != 200 {
				t.Fatal("env edit failed")
			}
			passed := ready(sb.ID, sample, "env_edit")
			sample["env_edit_wait_ms"] = time.Since(envAt).Milliseconds()
			if !passed {
				t.Fatal("env restart failed HTTP and transformed env-value readiness")
			}
			if !cleanup(sb.ID) {
				t.Fatal("edit fixture cleanup failed")
			}
			continue
		}
		started = time.Now()
		stopped := request("POST", "/v1/sandboxes/"+sb.ID+"/stop", "")
		if stopped.Code != 200 {
			sample["failure"] = "stop"
			t.Fatal("stop failed")
		}
		sample["stop_ms"] = time.Since(started).Milliseconds()
		resumedAt := time.Now()
		resumed := request("POST", "/v1/sandboxes/"+sb.ID+"/start", "")
		if resumed.Code != 200 {
			sample["failure"] = "resume"
			t.Fatal("resume failed")
		}
		sample["resume_api_ms"] = time.Since(resumedAt).Milliseconds()
		if !ready(sb.ID, sample, "resume") {
			t.Fatal("resume HTTP/asset readiness failed")
		}
		sample["resume_ready_ms"] = time.Since(resumedAt).Milliseconds()
		sample["stop_resume_ready_ms"] = time.Since(started).Milliseconds()
		marker := fmt.Sprintf("lifecycle fixture iteration %d\n", iteration)
		if edited := request("PUT", "/v1/sandboxes/"+sb.ID+"/files?path=lifecycle-marker.md", marker); edited.Code != 200 {
			sample["failure"] = fmt.Sprintf("fixture edit HTTP%d", edited.Code)
			t.Fatal("fixture edit failed")
		}
		started = time.Now()
		if backend == "docker" {
			if request("POST", "/v1/sandboxes/"+sb.ID+"/stop", "").Code != 200 {
				t.Fatal("publish stop failed")
			}
		}
		snapshotAt := time.Now()
		published := request("POST", "/v1/snapshots", `{"source_sandbox_id":"`+sb.ID+`","name":"Disposable lifecycle snapshot"}`)
		var snap v1Snapshot
		if published.Code != 201 || json.Unmarshal(published.Body.Bytes(), &snap) != nil {
			sample["failure"] = fmt.Sprintf("publish HTTP%d", published.Code)
			t.Fatal("publish failed")
		}
		sample["publish_handler_ms"] = time.Since(snapshotAt).Milliseconds()
		if backend == "docker" {
			if request("POST", "/v1/sandboxes/"+sb.ID+"/start", "").Code != 200 {
				t.Fatal("publish restart failed")
			}
		}
		if !ready(sb.ID, sample, "publish_source") {
			t.Fatal("source not ready after publish")
		}
		sample["publish_source_ready_ms"] = time.Since(started).Milliseconds()
		sample["publish_stopped_source"] = backend == "docker"
		if os.Getenv("APP_BENCH_SKIP_REMIX") == "1" {
			if !cleanup(sb.ID) {
				t.Fatal("source cleanup failed")
			}
			continue
		}
		started = time.Now()
		forked := request("POST", "/v1/apps/"+app.ID+"/fork", `{"snapshot_id":"`+snap.ID+`","external_user_id":"synthetic-remix-owner"}`)
		var fork struct {
			App     v1App       `json:"app"`
			Sandbox sandboxResp `json:"sandbox"`
			Error   string      `json:"sandbox_error"`
		}
		_ = json.Unmarshal(forked.Body.Bytes(), &fork)
		if fork.Sandbox.ID != "" {
			owned[fork.Sandbox.ID] = true
		}
		if forked.Code != 201 || fork.Error != "" || fork.Sandbox.ID == "" {
			sample["failure"] = fmt.Sprintf("remix HTTP%d", forked.Code)
			t.Fatal("remix failed")
		}
		sample["remix_api_ms"] = time.Since(started).Milliseconds()
		if !ready(fork.Sandbox.ID, sample, "remix") {
			t.Fatal("remix HTTP/asset readiness failed")
		}
		sample["remix_ready_ms"] = time.Since(started).Milliseconds()
		got := request("GET", "/v1/sandboxes/"+fork.Sandbox.ID+"/files/content?path=lifecycle-marker.md", "")
		if got.Code != 200 || got.Body.String() != marker {
			t.Fatal("remix lost source marker")
		}
		sample["remix_source_verified"] = true
		if !cleanup(fork.Sandbox.ID) || !cleanup(sb.ID) {
			t.Fatal("iteration cleanup failed")
		}
		sample["completed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
}
func bytesContainsAny(body []byte, values []string) bool {
	for _, value := range values {
		if strings.Contains(string(body), value) {
			return true
		}
	}
	return false
}
