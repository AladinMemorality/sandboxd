package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Explicitly invoked disposable functional harness, not production startup or
// deployment isolation acceptance. No guest NIC or public egress is enabled.
func TestOperatorCubeAppLifecycle(t *testing.T) {
	if os.Getenv("CUBE_OPERATOR_FUNCTIONAL") != "1" {
		t.Skip("explicit operator fixture only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var secret struct {
		CubeKey string `json:"cube_key"`
	}
	data, err := os.ReadFile("/root/cube-pilot/test-secrets.json")
	if err != nil {
		t.Fatal("Cube fixture credentials unavailable")
	}
	if json.Unmarshal(data, &secret) != nil || secret.CubeKey == "" {
		t.Fatal("Cube fixture credentials invalid")
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
	s.CubeProxyURL = "http://127.0.0.1:80"
	s.CubeDomain = "cube.app"
	s.CubeAgentRelayOrigin = "https://functional.invalid"
	s.AgentProxyURL = "http://127.0.0.1:1"
	s.CubeTemplates = map[string]string{"react-pro": "tpl-ce9efc43b71248d9a0adfb90"}
	s.CubeApps = map[string]bool{project: true}
	app := &store.App{ID: project, OwnerToken: cfgTenant, Name: "Synthetic Cube lifecycle", ExternalUserID: sql.NullString{String: "synthetic-owner", Valid: true}}
	if err = s.Store.CreateApp(ctx, app); err != nil {
		t.Fatal("fixture app persistence failed")
	}
	report := map[string]any{"production_startup_accepted": false, "network_isolation_accepted": false, "template": "tpl-ce9efc43b71248d9a0adfb90"}
	t.Cleanup(func() {
		deleted := true
		for _, appID := range ownedApps {
			current, e := s.Store.CurrentSandboxForApp(context.Background(), appID)
			if e != nil && !errors.Is(e, store.ErrNotFound) {
				deleted = false
				t.Errorf("cleanup lookup failed: %v", e)
			}
			if e == nil {
				s.stopCubeEgress(current.ID)
				response := cubeRequest(s, "DELETE", "/v1/sandboxes/"+current.ID, "", cfgTenant)
				if response.Code != 204 {
					deleted = false
					t.Errorf("cleanup HTTP %d", response.Code)
				}
			}
		}
		report["all_vms_deleted"] = deleted
		encoded, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile("/root/cube-claude-acceptance/lifecycle-report.json", encoded, 0600)
	})
	if err = s.ConfigureCubeEgress(ctx, CubeEgressConfig{Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}, BridgeURL: "https://functional.invalid/api/bridge"}); err != nil {
		t.Fatal("reverse fixture configuration failed")
	}
	// No fixed-service calls or model requests are made in this fixture.

	started := time.Now()
	created := cubeRequest(s, "POST", "/v1/apps/"+project+"/sandbox", `{"runtime_preset":"react-pro"}`, cfgTenant)
	if created.Code != 201 {
		t.Fatalf("functional Cube create HTTP %d", created.Code)
	}
	report["create_ms"] = time.Since(started).Milliseconds()
	var sb sandboxResp
	if json.Unmarshal(created.Body.Bytes(), &sb) != nil || sb.ID == "" {
		t.Fatal("create returned no stable ID")
	}

	client := s.runtimeClientFor(sb.ID)
	ready := func(id string) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			status, e := s.runtimeClientFor(id).Status(ctx)
			if e == nil && string(status.Preview.Status) == "ready" {
				return
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
	report["remix_ms"] = time.Since(started).Milliseconds()
	ready(fork.Sandbox.ID)
	report["remix_ready_ms"] = time.Since(started).Milliseconds()
	t.Log("remix ready")
	forkClient := s.runtimeClientFor(fork.Sandbox.ID)
	data, e = forkClient.ReadFile(ctx, "migration-marker.md")
	if e != nil || string(data) != "published source\n" {
		t.Fatal("remix source bytes mismatch")
	}
	if _, e = forkClient.ReadFile(ctx, "secrets.json"); e == nil {
		t.Fatal("remix inherited private .env")
	}
	sourceBinding, e := s.Store.GetRuntimeBinding(ctx, sb.ID)
	if e != nil {
		t.Fatal(e)
	}
	forkBinding, e := s.Store.GetRuntimeBinding(ctx, fork.Sandbox.ID)
	if e != nil {
		t.Fatal(e)
	}
	sourceToken, _ := s.Secrets.Open(sourceBinding.TokenCiphertext, sourceBinding.TokenNonce)
	forkToken, _ := s.Secrets.Open(forkBinding.TokenCiphertext, forkBinding.TokenNonce)
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
	if _, e = client.PutFile(ctx, "migration-marker.md", strings.NewReader("owner later edit\n")); e != nil {
		t.Fatal(e)
	}
	restored := cubeRequest(s, "POST", "/v1/apps/"+project+"/restore", `{"snapshot_id":"`+snap.ID+`"}`, cfgTenant)
	if restored.Code != 201 {
		t.Fatalf("restore HTTP %d: %s", restored.Code, restored.Body.String())
	}
	current, e := s.Store.CurrentSandboxForApp(ctx, project)
	if e != nil || current.ID == sb.ID || current.AppID.String != project {
		t.Fatal("restore identity contract failed")
	}
	ready(current.ID)
	data, e = s.runtimeClientFor(current.ID).ReadFile(ctx, "migration-marker.md")
	if e != nil || string(data) != "published source\n" {
		t.Fatal("restore did not apply frozen source")
	}
	report["owner_restore_frozen_source_stable_app"] = true
	t.Log("PASS real Cube create, source publish, sanitized remix, pause/resume, owner restore; no model calls")
}
