package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/projectsource"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func TestProjectImportDisabledByDefault(t *testing.T) {
	t.Setenv("RUNTIMED_PROJECT_DEPLOYMENTS", "")
	a := &app{}
	w := httptest.NewRecorder()
	a.controlHandler().ServeHTTP(w, httptest.NewRequest("PUT", "/import/project-source", strings.NewReader("")))
	if w.Code != 404 {
		t.Fatalf("disabled importer: %d", w.Code)
	}
}

func TestProjectGuestRebuildsLockedDependenciesAndServesPage(t *testing.T) {
	if os.Geteuid() != 1000 || os.Getenv("RUNTIMED_CUBE_GUEST") != "1" {
		t.Skip("requires isolated guest UID1000")
	}
	base, e := os.MkdirTemp("/home/sandbox/workspace", "project-rebuild-test-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(base)
	src := filepath.Join(base, "source")
	dest := filepath.Join(base, "destination")
	os.MkdirAll(filepath.Join(src, "packages/message"), 0755)
	os.Mkdir(dest, 0755)
	files := map[string]string{
		"package.json":                  `{"name":"portable-fixture","version":"1.0.0","dependencies":{"fixture-message":"file:packages/message"}}`,
		"packages/message/package.json": `{"name":"fixture-message","version":"1.0.0","main":"index.js"}`,
		"packages/message/index.js":     `module.exports = "rebuilt portable project";`,
		"server.js":                     `require('http').createServer((req,res)=>res.end(require('fixture-message'))).listen(3000,'127.0.0.1');`,
		"sandbox.yaml":                  "version: 1\nweb:\n  command: node server.js\n  port: 3000\n  health_path: /\n",
	}
	for name, body := range files {
		if e = os.WriteFile(filepath.Join(src, name), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "install", "--package-lock-only", "--ignore-scripts", "--offline")
	cmd.Dir = src
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + base, "npm_config_audit=false", "npm_config_fund=false"}
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture lock generation: %v %s", e, out)
	}
	lock, e := os.ReadFile(filepath.Join(src, "package-lock.json"))
	if e != nil {
		t.Fatal(e)
	}
	files["package-lock.json"] = string(lock)
	var input bytes.Buffer
	zw := zip.NewWriter(&input)
	for name, body := range files {
		f, _ := zw.Create(name)
		f.Write([]byte(body))
	}
	zw.Close()
	recipe := projectsource.Recipe{Version: 1, RuntimeImage: "example/runtime@sha256:" + strings.Repeat("a", 64), PackageManager: "npm", Lockfile: "package-lock.json", Install: []string{"npm", "ci", "--no-audit", "--no-fund"}, Start: []string{"sh", "-lc", "node server.js"}, Port: 3000, HealthPath: "/"}
	source, m, e := projectsource.Build(input.Bytes(), recipe)
	if e != nil {
		t.Fatal(e)
	}
	if e = projectsource.Verify(source, m); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e = runtime.InstallPrivateWorkspacePrepared(dest, source, func(staged string) error {
		if e := validateProjectCommands(staged, recipe); e != nil {
			return e
		}
		return prepareSourceDependencies(ctx, staged, dest)
	}); e != nil {
		t.Fatal(e)
	}
	server := exec.CommandContext(ctx, "node", "server.js")
	server.Dir = dest
	server.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + base}
	if e = server.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	client := &http.Client{Timeout: time.Second}
	for time.Since(start) < 20*time.Second {
		resp, e := client.Get("http://127.0.0.1:3000/")
		if e == nil {
			b, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			if e == nil && string(b) == "rebuilt portable project" {
				t.Logf("source import + locked npm install + healthy HTTP: %s", time.Since(start))
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("rebuilt page did not become healthy")
}
func TestProjectImportRequiresFencedGuest(t *testing.T) {
	t.Setenv("RUNTIMED_PROJECT_DEPLOYMENTS", "canary")
	a := &app{}
	w := httptest.NewRecorder()
	a.controlHandler().ServeHTTP(w, httptest.NewRequest("PUT", "/import/project-source", strings.NewReader("")))
	if w.Code != 409 {
		t.Fatalf("unfenced importer: %d", w.Code)
	}
}
func TestProjectGuestSourceImportRoundtrip(t *testing.T) {
	if os.Geteuid() != 1000 || os.Getenv("RUNTIMED_CUBE_GUEST") != "1" {
		t.Skip("requires isolated guest UID1000")
	}
	t.Setenv("RUNTIMED_PROJECT_DEPLOYMENTS", "canary")
	image := "example/runtime@sha256:" + strings.Repeat("a", 64)
	t.Setenv("RUNTIMED_PROJECT_IMAGE", image)
	base, e := os.MkdirTemp("/home/sandbox/workspace", "project-import-test-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(base)
	root := filepath.Join(base, "app")
	if e = os.Mkdir(root, 0755); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "old.txt"), []byte("old"), 0600)
	recipe := projectsource.Recipe{Version: 1, RuntimeImage: image, PackageManager: "none", Start: []string{"sh", "-lc", "python3 -m http.server 3000"}, Port: 3000, HealthPath: "/"}
	var zipBody bytes.Buffer
	zw := zip.NewWriter(&zipBody)
	for name, body := range map[string]string{"index.html": "restored project", "sandbox.yaml": "version: 1\nweb:\n  command: python3 -m http.server 3000\n  port: 3000\n  health_path: /\n"} {
		f, e := zw.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(body))
	}
	zw.Close()
	source, m, e := projectsource.Build(zipBody.Bytes(), recipe)
	if e != nil {
		t.Fatal(e)
	}
	var payload bytes.Buffer
	mw := multipart.NewWriter(&payload)
	part, _ := mw.CreateFormField("manifest")
	json.NewEncoder(part).Encode(m)
	part, _ = mw.CreateFormFile("source", "source.zip")
	part.Write(source)
	mw.Close()
	a := &app{appDir: root, runtimeDir: t.TempDir(), requestRestart: func() {}}
	handler := a.controlHandler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/workspace/quiesce", nil))
	if w.Code != 200 {
		t.Fatalf("quiesce: %d %s", w.Code, w.Body)
	}
	req := httptest.NewRequest("PUT", "/import/project-source", bytes.NewReader(payload.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	body, e := os.ReadFile(filepath.Join(root, "index.html"))
	if e != nil || string(body) != "restored project" {
		t.Fatalf("restored source: %v", e)
	}
	if !a.workspaceQuiesced {
		t.Fatal("import resumed writes before coordinator verification")
	}
	if _, e = os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(e) {
		t.Fatal("old source merged into new revision")
	}
}
