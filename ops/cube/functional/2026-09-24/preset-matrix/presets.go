package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"os"
	"strings"
	"time"
)

func presetReady(ctx context.Context, g *guest, preset string) *rt.Status {
	if preset != "worker" {
		return ready(ctx, g)
	}
	for i := 0; i < 150; i++ {
		s, e := g.client.Status(ctx)
		if e == nil && s.Preview.Status == rt.PreviewNone && len(s.Processes) == 1 && s.Processes[0].Running && s.Processes[0].Pid > 0 {
			return s
		}
		time.Sleep(100 * time.Millisecond)
	}
	panic("worker process not running")
}
func runPreset(template, preset, reportPath string) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("preset %s fixture failed: %v", preset, p)
		}
	}()
	if _, e := os.Stat("/root/bench-ready"); e != nil {
		return errors.New("not isolated VM")
	}
	supported := map[string]bool{"react-pro": true, "marketplace": true, "react-vite": true, "nextjs": true, "node-express": true, "fastapi": true, "worker": true}
	if !supported[preset] {
		return errors.New("unsupported fixture preset")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var secrets map[string]string
	b, e := os.ReadFile("/root/cube-pilot/test-secrets.json")
	require(e)
	require(json.Unmarshal(b, &secrets))
	c, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:3000", APIKey: secrets["cube_key"]})
	require(e)
	entropy := make([]byte, 32)
	_, e = rand.Read(entropy)
	require(e)
	g := &guest{token: hex.EncodeToString(entropy)}
	g.sb, e = c.Create(ctx, cube.CreateRequest{TemplateID: template, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": g.token}, TimeoutSeconds: 600, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}})
	require(e)
	report := map[string]any{"preset": preset, "template": template, "sandbox_id": g.sb.SandboxID}
	defer func() {
		if g.cancel != nil {
			g.cancel()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if e := c.Delete(cleanup, g.sb.SandboxID); e != nil {
			err = fmt.Errorf("fixture cleanup failed: %s", g.sb.SandboxID)
		} else {
			_, e := c.Get(cleanup, g.sb.SandboxID)
			var apiErr *cube.APIError
			deleted := errors.As(e, &apiErr) && apiErr.StatusCode == 404
			report["delete_verified_http404"] = deleted
			if !deleted {
				err = fmt.Errorf("deleted fixture still resolvable: %s", g.sb.SandboxID)
			}
		}
		encoded, e := json.MarshalIndent(report, "", "  ")
		if e == nil {
			e = os.WriteFile(reportPath, append(encoded, '\n'), 0600)
		}
		if e != nil {
			err = fmt.Errorf("cannot write final fixture evidence: %v", e)
		}
	}()
	g.client = remote(g)
	attach(ctx, g)
	initial := presetReady(ctx, g, preset)
	checks := []string{"authenticated_supervisor", "authenticated_reverse_channel", "expected_process_running"}
	switch preset {
	case "worker":
		checks = append(checks, "worker_only_no_preview")
	case "react-pro", "marketplace", "react-vite":
		if !strings.Contains(string(page(g, "/")), "/src/main.tsx") || !strings.Contains(string(page(g, "/src/main.tsx")), "createRoot") {
			panic("React frontend entrypoint unavailable")
		}
		page(g, "/@vite/client")
		checks = append(checks, "react_html_and_transformed_entrypoint")
	case "nextjs":
		if !strings.Contains(string(page(g, "/")), "/_next/") {
			panic("Next frontend unavailable")
		}
		checks = append(checks, "next_server_rendered_html")

	default:
		if !strings.Contains(string(page(g, "/health")), `"status":"ok"`) {
			panic("API health mismatch")
		}
		page(g, "/")
		checks = append(checks, "backend_health_and_root_routes")
	}
	g.cancel()
	require(c.Pause(ctx, g.sb.SandboxID))
	_, e = c.Connect(ctx, g.sb.SandboxID, cube.ConnectRequest{TimeoutSeconds: 600})
	require(e)
	attach(ctx, g)
	after := presetReady(ctx, g, preset)
	if !initial.Runtimed.BootedAt.Equal(after.Runtimed.BootedAt) || len(initial.Processes) != len(after.Processes) {
		panic("resume restarted process")
	}
	for i, p := range initial.Processes {
		q := after.Processes[i]
		if p.Pid != q.Pid || p.Restarts != q.Restarts || !q.Running {
			panic("resume changed process identity")
		}
	}
	if preset != "worker" {
		page(g, "/")
	}
	checks = append(checks, "pause_resume_same_supervisor_and_process")
	fmt.Printf("PASS preset %s startup/resume; checking exports\n", preset)
	require(g.client.QuiesceWorkspace(ctx))
	m := rt.HomeManifest{Version: 2, Entries: []rt.HomeManifestEntry{{Path: ".runtimed", Disposition: "separate"}, {Path: "workspace/app", Disposition: "separate"}, {Path: ".bashrc", Disposition: "preserve"}, {Path: ".profile", Disposition: "preserve"}, {Path: ".bash_logout", Disposition: "preserve"}, {Path: ".cache", Disposition: "preserve"}}}
	if preset == "nextjs" {
		m.Entries = append(m.Entries, rt.HomeManifestEntry{Path: ".config/nextjs-nodejs", Disposition: "preserve"})
	}
	home, e := os.CreateTemp("/data", "preset-home-")
	require(e)
	defer os.Remove(home.Name())
	defer home.Close()
	require(g.client.ExportPrivateHome(ctx, m, home))
	fmt.Printf("PASS preset %s home export\n", preset)
	hd := homeDigest(m, home)
	checks = append(checks, "quiesced_home_v2_export_digest")
	app, e := os.CreateTemp("/data", "preset-app-")
	require(e)
	defer os.Remove(app.Name())
	defer app.Close()
	require(g.client.ExportPrivateWorkspaceFile(ctx, app))
	ad := appDigest(app)
	checks = append(checks, "quiesced_workspace_v2_export_digest")
	report = map[string]any{"preset": preset, "template": template, "sandbox_id": g.sb.SandboxID, "checks": checks, "home_archive_bytes": fileSize(home), "workspace_archive_bytes": fileSize(app), "home_digest": hd, "workspace_digest": ad, "scope": "single fresh authenticated Cube starter, ordinary frontend/backend/worker readiness, same-process resume, quiesced home/workspace export; not real-owner ABI or performance benchmark"}
	b, e = json.MarshalIndent(report, "", "  ")
	require(e)
	require(os.WriteFile(reportPath, append(b, '\n'), 0600))
	fmt.Printf("PASS preset %s all functional checks\n", preset)
	return nil
}
