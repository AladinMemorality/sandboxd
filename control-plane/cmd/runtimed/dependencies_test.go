package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDependencyCommandSelectsLockedManager(t *testing.T) {
	for _, tt := range []struct{ lock, cmd, flag string }{{"pnpm-lock.yaml", "pnpm", "--frozen-lockfile"}, {"package-lock.json", "npm", "ci"}, {"npm-shrinkwrap.json", "npm", "ci"}, {"bun.lock", "bun", "--frozen-lockfile"}, {"yarn.lock", "", ""}} {
		t.Run(tt.lock, func(t *testing.T) {
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"test":"1.0.0"}}`), 0644)
			os.WriteFile(filepath.Join(root, tt.lock), []byte("lock"), 0644)
			cmd, args, e := nodeDependencyCommand(root)
			if tt.cmd == "" {
				if e == nil {
					t.Fatal("unsupported manager accepted")
				}
				return
			}
			if e != nil || cmd != tt.cmd || !strings.Contains(strings.Join(args, " "), tt.flag) {
				t.Fatalf("%s %v %v", cmd, args, e)
			}
		})
	}
}
func TestDependencyPreparationFailureKeepsOriginalTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	os.Mkdir(root, 0755)
	os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0644)
	data := importTestZip(t, map[string]string{"package.json": `{"dependencies":{"changed":"1"}}`})
	failed := errors.New("controlled install failure")
	e := replaceSourcePrepared(context.Background(), root, data, func(ctx context.Context, staged, final string) error {
		os.Mkdir(filepath.Join(staged, "node_modules"), 0755)
		return failed
	})
	if !errors.Is(e, failed) {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "keep.txt"))
	if string(b) != "keep" {
		t.Fatal("failed preparation modified live tree")
	}
}
func TestDependencyInstallRealNpmLocalFixtureSterileEnvironment(t *testing.T) {
	if _, e := exec.LookPath("npm"); e != nil {
		t.Skip("requires npm: run in the dependency integration image")
	}
	source := t.TempDir()
	os.MkdirAll(filepath.Join(source, "packages/local"), 0755)
	files := map[string]string{"package.json": `{"name":"fixture","version":"1.0.0","dependencies":{"local":"file:packages/local"},"scripts":{"postinstall":"node -e \"require('fs').writeFileSync('env-check.txt', String(!!process.env.OWNER_SECRET || !!process.env.RUNTIMED_HTTP_TOKEN || !!process.env.ANTHROPIC_API_KEY))\""}}`, "packages/local/package.json": `{"name":"local","version":"1.0.0","main":"index.js"}`, "packages/local/index.js": `module.exports = 42`}
	for name, body := range files {
		os.WriteFile(filepath.Join(source, name), []byte(body), 0644)
	}
	cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = source
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture lock: %v %s", e, out)
	}
	lock, _ := os.ReadFile(filepath.Join(source, "package-lock.json"))
	files["package-lock.json"] = string(lock)
	t.Setenv("OWNER_SECRET", "must not inherit")
	t.Setenv("RUNTIMED_HTTP_TOKEN", "must not inherit")
	t.Setenv("ANTHROPIC_API_KEY", "must not inherit")
	root := filepath.Join(t.TempDir(), "app")
	os.Mkdir(root, 0755)
	os.Mkdir(filepath.Join(root, "node_modules"), 0755)
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"old":"1"}}`), 0644)
	if e := replaceSourcePrepared(context.Background(), root, importTestZip(t, files), prepareSourceDependencies); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "env-check.txt"))
	if string(b) != "false" {
		t.Fatalf("install inherited credentials: %q", b)
	}
	cmd = exec.Command("node", "-e", `if(require('local')!==42)process.exit(1)`)
	cmd.Dir = root
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("installed module: %v %s", e, out)
	}
}
func TestDependencyCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	e := runDependencyCommand(ctx, t.TempDir(), []string{"PATH=/usr/bin:/bin"}, "sh", "-c", "sleep 30 & wait")
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation failed: %v", e)
	}
}
func TestDependencyInstallRealPnpmLocalFixture(t *testing.T) {
	if _, e := exec.LookPath("pnpm"); e != nil {
		t.Skip("requires pnpm in the sandbox base image")
	}
	source := t.TempDir()
	os.MkdirAll(filepath.Join(source, "packages/local"), 0755)
	files := map[string]string{"package.json": `{"name":"fixture","version":"1.0.0","dependencies":{"local":"file:packages/local"}}`, "packages/local/package.json": `{"name":"local","version":"1.0.0","main":"index.js"}`, "packages/local/index.js": `module.exports = 43`}
	for name, body := range files {
		os.WriteFile(filepath.Join(source, name), []byte(body), 0644)
	}
	cmd := exec.Command("pnpm", "install", "--lockfile-only", "--ignore-scripts")
	cmd.Dir = source
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture lock: %v %s", e, out)
	}
	lock, _ := os.ReadFile(filepath.Join(source, "pnpm-lock.yaml"))
	files["pnpm-lock.yaml"] = string(lock)
	root := filepath.Join(t.TempDir(), "app")
	os.Mkdir(root, 0755)
	if e := replaceSourcePrepared(context.Background(), root, importTestZip(t, files), prepareSourceDependencies); e != nil {
		t.Fatal(e)
	}
	cmd = exec.Command("node", "-e", `if(require('local')!==43)process.exit(1)`)
	cmd.Dir = root
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("installed module: %v %s", e, out)
	}
}
func TestSourceKeepsMatchingFreshPythonEnvironment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	os.MkdirAll(filepath.Join(root, ".venv/bin"), 0755)
	os.WriteFile(filepath.Join(root, ".venv/bin/python"), []byte("fresh python"), 0755)
	os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("fastapi==1"), 0644)
	data := importTestZip(t, map[string]string{"requirements.txt": "fastapi==1", "main.py": "print('hello')"})
	if e := replaceSourcePrepared(context.Background(), root, data, prepareSourceDependencies); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".venv/bin/python"))
	if string(b) != "fresh python" {
		t.Fatal("fresh Python environment not retained")
	}
}
func TestPrivateWorkspaceRejectsNonCubeUIDQuiescence(t *testing.T) {
	t.Setenv("RUNTIMED_CUBE_GUEST", "")
	if e := stopWorkspaceWriters(context.Background()); e == nil {
		t.Fatal("ordinary runtime could stop unrelated UID processes")
	}
}
