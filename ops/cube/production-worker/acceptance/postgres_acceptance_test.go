package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	guestRuntime "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Explicitly invoked disposable functional harness, not production startup or
// deployment isolation acceptance. No guest NIC or public egress is enabled.
func TestOperatorCubePostgresLifecycle(t *testing.T) {
	if os.Getenv("CUBE_POSTGRES_FUNCTIONAL") != "1" {
		t.Skip("explicit operator fixture only")
	}
	if hostname, err := os.Hostname(); err != nil || hostname != "baarcha-cube-worker-01" {
		t.Fatal("fresh worker required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var secret struct {
		CubeKey string `json:"cube_key"`
	}
	data, err := os.ReadFile("/root/cube-production/test-secrets.json")
	if err != nil {
		t.Fatal("Cube fixture credentials unavailable")
	}
	if json.Unmarshal(data, &secret) != nil || secret.CubeKey == "" {
		t.Fatal("Cube fixture credentials invalid")
	}
	stage := os.Getenv("CUBE_POSTGRES_STAGE")
	if !filepath.IsAbs(stage) {
		t.Fatal("absolute evidence directory required")
	}
	template := os.Getenv("CUBE_POSTGRES_TEMPLATE")
	if !strings.HasPrefix(template, "tpl-") {
		t.Fatal("reviewed template required")
	}
	project := newULID()
	s, _ := newConfigTestServer(t)
	s.Locks = idlock.New()
	s.LibraryRoot = t.TempDir()
	ownedApps := []string{project}
	s.Cube, err = cube.New(cube.Config{APIURL: "http://127.0.0.1:3000", APIKey: secret.CubeKey})
	if err != nil {
		t.Fatal("Cube client unavailable")
	}
	if err = s.Cube.ConfigureAdmission(ctx, s.Store, cube.AdmissionConfig{MaxActive: 12, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{template: {CPUCount: 2, MemoryMB: 2048}}}); err != nil {
		t.Fatal(err)
	}
	s.CubeProxyURL = "http://127.0.0.1:80"
	s.CubeDomain = "cube.app"
	s.CubeAgentRelayOrigin = "https://functional.invalid"
	s.AgentProxyURL = "http://127.0.0.1:1"
	s.CubeTemplates = map[string]string{"node-postgres": template}
	s.CubeApps = map[string]bool{project: true}
	s.CubeAllApps = true
	app := &store.App{ID: project, OwnerToken: cfgTenant, Name: "Synthetic Cube lifecycle", ExternalUserID: sql.NullString{String: "synthetic-owner", Valid: true}}
	if err = s.Store.CreateApp(ctx, app); err != nil {
		t.Fatal("fixture app persistence failed")
	}
	report := map[string]any{"production_startup_accepted": false, "network_isolation_accepted": false, "template": template}
	t.Cleanup(func() {
		deleted := true
		for _, appID := range ownedApps {
			if !deleteOperatorAcceptanceApp(t, s, appID) {
				deleted = false
			}
		}
		report["all_vms_deleted"] = deleted
		encoded, e := json.MarshalIndent(report, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(stage, "postgres-lifecycle-report.json"), encoded, 0600)
		}
		if e != nil {
			t.Errorf("report: %v", e)
		}
	})
	if err = s.ConfigureCubeEgress(ctx, CubeEgressConfig{Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}, BridgeURL: "https://functional.invalid/api/bridge"}); err != nil {
		t.Fatal("reverse fixture configuration failed")
	}
	// No fixed-service calls or model requests are made in this fixture.

	started := time.Now()
	created := cubeRequest(s, "POST", "/v1/apps/"+project+"/sandbox", `{"runtime_preset":"node-postgres"}`, cfgTenant)
	if created.Code != 201 {
		t.Fatalf("functional Cube create HTTP %d", created.Code)
	}
	recordOperatorAcceptanceIdentity(t, s, project, stage)
	report["create_ms"] = time.Since(started).Milliseconds()
	var sb sandboxResp
	if json.Unmarshal(created.Body.Bytes(), &sb) != nil || sb.ID == "" {
		t.Fatal("create returned no stable ID")
	}

	client := s.runtimeClientFor(sb.ID)
	httpClient := &http.Client{Timeout: 10 * time.Second}
	request := func(id, method, path, body string) (int, []byte) {
		t.Helper()
		binding, e := s.Store.GetRuntimeBinding(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		plain, e := s.Secrets.Open(binding.TokenCiphertext, binding.TokenNonce)
		if e != nil {
			t.Fatal(e)
		}
		var credentials cubeCredentials
		if json.Unmarshal(plain, &credentials) != nil {
			t.Fatal("invalid fixture binding")
		}
		r, e := http.NewRequestWithContext(ctx, method, "http://127.0.0.1"+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Host = fmt.Sprintf("3000-%s.cube.app", binding.RuntimeID)
		r.Header.Set("Cube-Traffic-Access-Token", credentials.TrafficAccessToken)
		r.Header.Set("Content-Type", "application/json")
		response, e := httpClient.Do(r)
		if e != nil {
			// A reexec can reset a health connection. Polling may retry;
			// mutation and data assertions still fail on the zero status.
			return 0, nil
		}
		defer response.Body.Close()
		data, e := io.ReadAll(io.LimitReader(response.Body, 65536))
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, data
	}
	assertNotes := func(id string, want int) {
		t.Helper()
		code, data := request(id, "GET", "/api/notes", "")
		var notes []map[string]any
		if code != 200 || json.Unmarshal(data, &notes) != nil || len(notes) != want {
			t.Fatalf("notes HTTP%d expected%d entries", code, want)
		}
		if want == 1 && notes[0]["body"] != "persistent fixture note" {
			t.Fatal("private DB write changed")
		}
	}
	ready := func(id string) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			status, e := s.runtimeClientFor(id).Status(ctx)
			if e == nil && string(status.Preview.Status) == "ready" {
				// Resume/reexec can briefly retain the previous health observation.
				// The application's health handler performs SELECT 1 against the
				// currently running PostgreSQL worker, so require that real response.
				if code, data := request(id, "GET", "/health", ""); code == 200 && bytes.Contains(data, []byte(`"status":"ok"`)) {
					return
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		status, statusErr := s.runtimeClientFor(id).Status(ctx)
		encoded, _ := json.Marshal(status)
		report["failed_status"] = string(encoded)
		if statusErr != nil {
			report["status_error"] = statusErr.Error()
		}
		if status != nil {
			for _, p := range status.Processes {
				if logs, e := s.runtimeClientFor(id).ProcessLogs(ctx, p.Name, 20); e == nil {
					t.Logf("process %s logs: %+v", p.Name, logs)
				}
			}
		}
		t.Fatal("application frontend readiness deadline")
	}
	ready(sb.ID)
	report["create_ready_ms"] = time.Since(started).Milliseconds()
	t.Log("source ready")
	if code, _ := request(sb.ID, "POST", "/api/notes", `{"body":"persistent fixture note"}`); code != 201 {
		t.Fatalf("note create HTTP%d", code)
	}
	assertNotes(sb.ID, 1)
	report["database_write_read"] = true
	for path, content := range map[string]string{"migration-marker.md": "published source\n", "secrets.json": `{"private":true}`, "local.db": "private synthetic database"} {
		if _, err = client.PutFile(ctx, path, strings.NewReader(content)); err != nil {
			t.Fatalf("write fixture %s: %v", path, err)
		}
	}
	started = time.Now()
	published := cubeRequest(s, "POST", "/v1/snapshots", `{"source_sandbox_id":"`+sb.ID+`","name":"Synthetic published fixture"}`, cfgTenant)
	if published.Code != 201 {
		t.Fatalf("publish HTTP %d: %s", published.Code, published.Body.String())
	}
	report["publish_ms"] = time.Since(started).Milliseconds()
	var snap v1Snapshot
	if json.Unmarshal(published.Body.Bytes(), &snap) != nil || snap.ID == "" {
		t.Fatal("missing snapshot ID")
	}
	stored, e := s.Store.GetSnapshot(ctx, snap.ID)
	if e != nil {
		t.Fatal(e)
	}
	archive, e := os.ReadFile(stored.ImagePath)
	if e != nil {
		t.Fatal(e)
	}
	zipped, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if e != nil {
		t.Fatal(e)
	}
	marker := false
	for _, entry := range zipped.File {
		if entry.Name == "migration-marker.md" {
			marker = true
		}
		if entry.Name == "secrets.json" || entry.Name == "local.db" || strings.Contains(entry.Name, "node_modules/") {
			t.Fatalf("private/build artifact published: %s", entry.Name)
		}
	}
	if !marker {
		t.Fatal("published source marker missing")
	}
	report["source_archive_private_data_excluded"] = true
	started = time.Now()
	forked := cubeRequest(s, "POST", "/v1/apps/"+project+"/fork", `{"snapshot_id":"`+snap.ID+`","external_user_id":"remix-fixture-owner","external_project_id":"remix-fixture-project"}`, cfgTenant)
	var fork struct {
		App     v1App       `json:"app"`
		Sandbox sandboxResp `json:"sandbox"`
		Error   string      `json:"sandbox_error"`
	}
	if json.Unmarshal(forked.Body.Bytes(), &fork) == nil && fork.App.ID != "" {
		ownedApps = append(ownedApps, fork.App.ID)
	}
	if forked.Code != 201 || fork.Error != "" || fork.Sandbox.ID == "" {
		t.Fatalf("fork HTTP %d: %s", forked.Code, fork.Error)
	}
	recordOperatorAcceptanceIdentity(t, s, fork.App.ID, stage)
	report["remix_ms"] = time.Since(started).Milliseconds()
	ready(fork.Sandbox.ID)
	report["remix_ready_ms"] = time.Since(started).Milliseconds()
	t.Log("remix ready")
	assertNotes(fork.Sandbox.ID, 0)
	report["fresh_remix_database_empty"] = true
	forkClient := s.runtimeClientFor(fork.Sandbox.ID)
	data, e = forkClient.ReadFile(ctx, "migration-marker.md")
	if e != nil || string(data) != "published source\n" {
		t.Fatal("remix source bytes mismatch")
	}
	_, e = forkClient.ReadFile(ctx, "secrets.json")
	var missing *guestRuntime.ResponseError
	if !errors.As(e, &missing) || missing.StatusCode != http.StatusNotFound {
		t.Fatal("remix private file absence was not confirmed by HTTP404")
	}
	sourceBinding, e := s.Store.GetRuntimeBinding(ctx, sb.ID)
	if e != nil {
		t.Fatal(e)
	}
	forkBinding, e := s.Store.GetRuntimeBinding(ctx, fork.Sandbox.ID)
	if e != nil {
		t.Fatal(e)
	}
	sourceToken, e := s.Secrets.Open(sourceBinding.TokenCiphertext, sourceBinding.TokenNonce)
	if e != nil {
		t.Fatal("source credential decryption failed")
	}
	forkToken, e := s.Secrets.Open(forkBinding.TokenCiphertext, forkBinding.TokenNonce)
	if e != nil {
		t.Fatal("remix credential decryption failed")
	}
	if sourceBinding.RuntimeID == forkBinding.RuntimeID || bytes.Equal(sourceToken, forkToken) {
		t.Fatal("remix reused source runtime credentials")
	}
	report["remix_ready_new_identity"] = true
	ready(sb.ID)
	started = time.Now()
	for _, action := range []string{"stop", "start"} {
		r := cubeRequest(s, "POST", "/v1/sandboxes/"+sb.ID+"/"+action, "", cfgTenant)
		if r.Code != 200 {
			t.Fatalf("%s HTTP %d", action, r.Code)
		}
	}
	ready(sb.ID)
	after, e := s.Store.CurrentSandboxForApp(ctx, project)
	if e != nil || after.ID != sb.ID {
		t.Fatal("resume changed stable sandbox identity")
	}
	report["pause_resume_stable_identity"] = true
	report["pause_resume_ready_ms"] = time.Since(started).Milliseconds()
	t.Log("source resumed ready")
	assertNotes(sb.ID, 1)
	report["database_survives_pause_resume"] = true
	if e = client.ApplyAppConfig(ctx, guestRuntime.AppConfigRequest{Env: map[string]string{"POSTGRES_FIXTURE_REVISION": "one"}, Revision: "postgres-fixture-reexec"}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		status, e := client.Status(ctx)
		if e == nil && status.AppConfigRevision == "postgres-fixture-reexec" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("config reexec deadline")
		}
		time.Sleep(200 * time.Millisecond)
	}
	ready(sb.ID)
	assertNotes(sb.ID, 1)
	report["database_survives_supervisor_reexec"] = true
	manifestBytes, e := client.ReadFile(ctx, "sandbox.yaml")
	if e != nil || !bytes.Contains(manifestBytes, []byte("\nbuild:")) {
		t.Fatal("fixture manifest missing expected build section")
	}
	activatedManifest := strings.Replace(string(manifestBytes), "\nbuild:", "\n  - name: acceptance-worker\n    command: \"sleep 600\"\n    restart_after_task: false\nbuild:", 1)
	if _, e = client.PutFile(ctx, "sandbox.yaml", strings.NewReader(activatedManifest)); e != nil {
		t.Fatal(e)
	}
	beforeReload, e := client.Status(ctx)
	if e != nil {
		t.Fatal(e)
	}
	reloaded := cubeRequest(s, "POST", "/v1/sandboxes/"+sb.ID+"/recreate", `{"reload_manifest":true}`, cfgTenant)
	if reloaded.Code != 200 {
		t.Fatalf("manifest activation HTTP%d: %s", reloaded.Code, reloaded.Body.String())
	}
	ready(sb.ID)
	afterReload, e := client.Status(ctx)
	if e != nil {
		t.Fatal(e)
	}
	workerActive := false
	for _, process := range afterReload.Processes {
		if process.Name == "acceptance-worker" && process.Running && process.Pid > 0 {
			workerActive = true
		}
	}
	if !workerActive || afterReload.Runtimed.BootedAt.Equal(beforeReload.Runtimed.BootedAt) || !strings.Contains(afterReload.AppConfigRevision, ":manifest:") {
		t.Fatal("manifest activation did not reexec and start the added worker")
	}
	assertNotes(sb.ID, 1)
	repeated := cubeRequest(s, "POST", "/v1/sandboxes/"+sb.ID+"/recreate", `{"reload_manifest":true}`, cfgTenant)
	if repeated.Code != 200 {
		t.Fatalf("repeated manifest activation HTTP%d", repeated.Code)
	}
	afterRepeat, e := client.Status(ctx)
	if e != nil || !afterRepeat.Runtimed.BootedAt.Equal(afterReload.Runtimed.BootedAt) || afterRepeat.Preview.Pid != afterReload.Preview.Pid || afterRepeat.AppConfigRevision != afterReload.AppConfigRevision {
		t.Fatal("identical manifest reload restarted the supervisor or app")
	}
	assertNotes(sb.ID, 1)
	reloadBinding, e := s.Store.GetRuntimeBinding(ctx, sb.ID)
	if e != nil || reloadBinding.RuntimeID != sourceBinding.RuntimeID {
		t.Fatal("manifest activation replaced the VM")
	}
	report["manifest_activation_new_worker_preserves_database_vm"] = true
	report["identical_manifest_activation_idempotent"] = true
	if _, e = client.PutFile(ctx, "migration-marker.md", strings.NewReader("owner later edit\n")); e != nil {
		t.Fatal(e)
	}
	restored := cubeRequest(s, "POST", "/v1/apps/"+project+"/restore", `{"snapshot_id":"`+snap.ID+`"}`, cfgTenant)
	if restored.Code != 201 {
		t.Fatalf("restore HTTP %d: %s", restored.Code, restored.Body.String())
	}
	current, e := s.Store.CurrentSandboxForApp(ctx, project)
	if e != nil || current.ID != sb.ID || current.AppID.String != project {
		t.Fatal("restore identity contract failed")
	}
	restoredBinding, e := s.Store.GetRuntimeBinding(ctx, current.ID)
	if e != nil || restoredBinding.RuntimeID != sourceBinding.RuntimeID || !bytes.Equal(restoredBinding.TokenCiphertext, sourceBinding.TokenCiphertext) || !bytes.Equal(restoredBinding.TokenNonce, sourceBinding.TokenNonce) {
		t.Fatal("owner restore replaced the VM or its private transport credentials")
	}
	ready(current.ID)
	data, e = s.runtimeClientFor(current.ID).ReadFile(ctx, "migration-marker.md")
	if e != nil || string(data) != "published source\n" {
		t.Fatal("restore did not apply frozen source")
	}
	report["owner_restore_frozen_source_stable_app_sandbox_vm_credentials"] = true
	assertNotes(current.ID, 1)
	report["database_survives_owner_source_restore"] = true
	manifest := guestRuntime.HomeManifest{Version: 2, Entries: []guestRuntime.HomeManifestEntry{
		{Path: "workspace/app", Disposition: "separate"}, {Path: ".runtimed", Disposition: "separate"},
		{Path: ".bashrc", Disposition: "preserve"}, {Path: ".bash_logout", Disposition: "preserve"},
		{Path: ".profile", Disposition: "preserve"}, {Path: ".cache", Disposition: "preserve"},
		{Path: ".baarcha-postgres", Disposition: "preserve"},
	}}
	if e = client.QuiesceWorkspace(ctx); e != nil {
		t.Fatal(e)
	}
	var home bytes.Buffer
	if e = client.ExportPrivateHome(ctx, manifest, &home); e != nil {
		t.Fatal("cold home export", e)
	}
	digest, e := guestRuntime.PrivateHomeDigest(manifest, bytes.NewReader(home.Bytes()), int64(home.Len()))
	if e != nil {
		t.Fatal(e)
	}
	if e = client.ImportPrivateHome(ctx, manifest, bytes.NewReader(home.Bytes()), int64(home.Len())); e != nil {
		t.Fatal("cold home import", e)
	}
	if e = client.ResumeWorkspace(ctx); e != nil {
		t.Fatal(e)
	}
	ready(sb.ID)
	assertNotes(sb.ID, 1)
	report["postgres_home_v2_roundtrip"] = true
	report["home_archive_bytes"] = home.Len()
	report["home_digest"] = digest
	t.Log("PASS optional PostgreSQL: write/read, pause/resume, reexec, private source restore, fresh source remix, cold fullhome roundtrip; no model calls")
}

// Cleanup has its own deadline: the fixture context may already have expired.
// Capture the immutable remote ID before the API removes its local binding.
func deleteOperatorAcceptanceApp(t *testing.T, s *Server, appID string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	current, err := s.Store.CurrentSandboxForApp(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return true // No sandbox was persisted for this owned app.
	}
	if err != nil {
		t.Errorf("cleanup sandbox lookup: %v", err)
		return false
	}
	binding, err := s.Store.GetRuntimeBinding(ctx, current.ID)
	if err != nil || binding.RuntimeID == "" {
		t.Errorf("cleanup remote identity unavailable: %v", err)
		return false
	}
	s.stopCubeEgress(current.ID)
	r := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/"+current.ID, nil)
	r = r.WithContext(auth.WithActor(ctx, auth.Actor{Name: cfgTenant, Kind: "service"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("cleanup HTTP %d", w.Code)
		return false
	}
	_, err = s.Cube.Get(ctx, binding.RuntimeID)
	var absent *cube.APIError
	if !errors.As(err, &absent) || absent.StatusCode != http.StatusNotFound {
		t.Error("cleanup independent remote GET did not return HTTP404")
		return false
	}
	return true
}

func TestOperatorAcceptanceCleanupVerifiesRemoteDeletion(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	row, err := s.Store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var deleted, verified atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sandboxes/vm-tasks" {
			t.Errorf("unexpected cleanup path %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			deleted.Store(true)
			w.WriteHeader(204)
		case http.MethodGet:
			verified.Store(deleted.Load())
			w.WriteHeader(404)
		default:
			w.WriteHeader(405)
		}
	}))
	defer upstream.Close()
	s.Cube, err = cube.New(cube.Config{APIURL: upstream.URL, APIKey: "synthetic-test-only"})
	if err != nil {
		t.Fatal(err)
	}
	if !deleteOperatorAcceptanceApp(t, s, row.AppID.String) || !deleted.Load() || !verified.Load() {
		t.Fatal("cleanup omitted deletion or its independent remote verification")
	}
}

// Persist owned identities before lifecycle mutations; recovery must not depend
// on a temporary SQLite file that the test framework removes at exit.
func recordOperatorAcceptanceIdentity(t *testing.T, s *Server, appID, stage string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	row, err := s.Store.CurrentSandboxForApp(ctx, appID)
	if err != nil {
		t.Fatal("owned sandbox identity unavailable")
	}
	binding, err := s.Store.GetRuntimeBinding(ctx, row.ID)
	if err != nil || binding.RuntimeID == "" {
		t.Fatal("owned remote identity unavailable")
	}
	data, err := json.Marshal(map[string]string{"app_id": appID, "sandbox_id": row.ID, "runtime_id": binding.RuntimeID, "template_id": binding.TemplateID})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(stage, "owned-guests.ndjson"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal("owned identity journal unavailable")
	}
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		t.Fatal("owned identity journal write failed")
	}
	if err = f.Sync(); err != nil {
		t.Fatal("owned identity journal sync failed")
	}
}
