package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// An opt-in disposable nested-cluster fixture, deliberately outside CI. It uses
// the real deny-all HTTP broker without fixed-service credentials, DNS overrides,
// direct guest egress, policy exceptions, or protected-destination probes.
func TestOperatorCubeViteReload(t *testing.T) {
	if os.Getenv("CUBE_RELOAD_FUNCTIONAL") != "1" {
		t.Skip("explicit disposable cold reload fixture only")
	}
	if hostname, err := os.Hostname(); err != nil || hostname != "baarcha-cube-worker-01" {
		t.Fatal("fresh worker required")
	}
	stage := os.Getenv("CUBE_RELOAD_STAGE")
	if !filepath.IsAbs(stage) {
		t.Fatal("absolute private evidence directory required")
	}
	if _, err := os.Stat(filepath.Join(stage, "disposable-reload")); err != nil {
		t.Fatal("missing disposable stage marker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var secret struct {
		CubeKey string `json:"cube_key"`
	}
	data, err := os.ReadFile("/root/cube-production/test-secrets.json")
	if err != nil || json.Unmarshal(data, &secret) != nil || secret.CubeKey == "" {
		t.Fatal("nested fixture credentials unavailable")
	}
	s, _ := newConfigTestServer(t)
	s.Locks = idlock.New()
	s.Cube, err = cube.New(cube.Config{APIURL: "http://127.0.0.1:3000", APIKey: secret.CubeKey})
	if err != nil {
		t.Fatal("Cube fixture unavailable")
	}
	project := newULID()
	s.CubeProxyURL, s.CubeDomain = "http://127.0.0.1:80", "cube.app"
	s.CubeAgentRelayOrigin, s.AgentProxyURL = "https://functional.invalid", "http://127.0.0.1:1"
	template := os.Getenv("CUBE_RELOAD_TEMPLATE")
	if template == "" {
		t.Fatal("fresh reviewed template required")
	}
	s.CubeTemplates = map[string]string{"react-pro": template}
	if err = s.Cube.ConfigureAdmission(ctx, s.Store, cube.AdmissionConfig{MaxActive: 12, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{template: {CPUCount: 2, MemoryMB: 2048}}}); err != nil {
		t.Fatal(err)
	}
	s.CubeApps = map[string]bool{project: true}
	s.CubeAllApps = true
	if err = s.Store.CreateApp(ctx, &store.App{ID: project, OwnerToken: cfgTenant, Name: "Disposable Vite reload fixture"}); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"production_accepted": false, "network_isolation_accepted": false, "template": s.CubeTemplates["react-pro"], "direct_nic_egress_enabled": false}
	t.Cleanup(func() {
		deleted := deleteOperatorAcceptanceApp(t, s, project)
		report["vm_deleted"] = deleted
		encoded, e := json.MarshalIndent(report, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(stage, "reload-report.json"), encoded, 0600)
		}
		if e != nil {
			t.Errorf("report: %v", e)
		}
	})
	policy := egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}
	if err = s.ConfigureCubeEgress(ctx, CubeEgressConfig{Policy: policy, BridgeURL: "https://functional.invalid/api/bridge"}); err != nil {
		t.Fatal(err)
	}
	r := cubeRequest(s, "POST", "/v1/apps/"+project+"/sandbox", `{"runtime_preset":"react-pro"}`, cfgTenant)
	if r.Code != 201 {
		t.Fatalf("create HTTP %d", r.Code)
	}
	var sb sandboxResp
	if json.Unmarshal(r.Body.Bytes(), &sb) != nil || sb.ID == "" {
		t.Fatal("missing sandbox")
	}
	recordOperatorAcceptanceIdentity(t, s, project, stage)
	client := s.runtimeClientFor(sb.ID)
	source, err := client.ExportSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(stage, "reload-regression.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string][]byte{
		"sandbox.yaml":              []byte("version: 1\nweb:\n  command: RELOAD_USE_INSTALLED_PATCH=1 node reload-regression.mjs\n  port: 3000\nbuild:\n  command: ''\n"),
		"reload-regression.mjs":     script,
		"README.reload-fixture.txt": []byte("DISPOSABLE_COLD_RELOAD_ONLY\n"),
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, entry := range original.File {
		if _, replace := replacements[entry.Name]; replace {
			continue
		}
		out, e := zw.Create(entry.Name)
		if e != nil {
			t.Fatal(e)
		}
		in, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(out, in)
		in.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	for name, contents := range replacements {
		out, e := zw.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = out.Write(contents); e != nil {
			t.Fatal(e)
		}
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = client.ImportSource(ctx, archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		if status, err := client.Status(ctx); err == nil && status.Preview.Restarts >= 5 && status.Preview.Pid == 0 {
			if logs, logErr := client.ProcessLogs(ctx, "web", 30); logErr == nil {
				data, _ := json.MarshalIndent(logs, "", "  ")
				_ = os.WriteFile(filepath.Join(stage, "reload-failed-process-log.json"), data, 0600)
			}
			t.Fatal("disposable reload script failed to start; bounded log retained")
		}
		data, e := client.ReadFile(ctx, "reload-results.json")
		if e == nil {
			var result map[string]any
			if json.Unmarshal(data, &result) != nil {
				t.Fatal("invalid guest result")
			}
			report["guest"] = result
			if result["complete"] != true {
				t.Fatalf("reload regression failed: %v", result["error"])
			}
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("reload fixture deadline")
}
