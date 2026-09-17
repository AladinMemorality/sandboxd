package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func importTestZip(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, content := range files {
		f, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		io.WriteString(f, content)
	}
	z.Close()
	return b.Bytes()
}
func TestImportAtomicallyReplacesAppAndKeepsRuntimeSecretsOutside(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	os.Mkdir(root, 0755)
	os.WriteFile(filepath.Join(root, "old.txt"), []byte("old"), 0644)
	runtimeDir := t.TempDir()
	os.WriteFile(filepath.Join(runtimeDir, "credential"), []byte("OWN_GUEST_TOKEN"), 0600)
	restart := make(chan struct{}, 1)
	a := &app{appDir: root, runtimeDir: runtimeDir, requestRestart: func() { restart <- struct{}{} }}
	data := importTestZip(t, map[string]string{"src/new.ts": "new source", "package.json": "{}", ".env": "CREATOR_SECRET", "data/private.json": "CREATOR_DATA"})
	req := httptest.NewRequest(http.MethodPut, "/import/source", bytes.NewReader(data))
	out := httptest.NewRecorder()
	a.handleSourceImport(out, req)
	if out.Code != 200 {
		t.Fatalf("import %d %s", out.Code, out.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "src/new.ts")); string(b) != "new source" {
		t.Fatal("source absent")
	}
	for _, name := range []string{"old.txt", ".env", "data/private.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(runtimeDir, "credential")); string(b) != "OWN_GUEST_TOKEN" {
		t.Fatal("guest identity changed")
	}
	select {
	case <-restart:
	case <-time.After(time.Second):
		t.Fatal("restart not requested")
	}
	if !a.restartPending {
		t.Fatal("tasks not gated during restart")
	}
}
func TestImportRejectsTraversalBeforeTouchingExistingApp(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0644)
	a := &app{appDir: root, requestRestart: func() { t.Error("unexpected restart") }}
	req := httptest.NewRequest("PUT", "/import/source", bytes.NewReader(importTestZip(t, map[string]string{"../escape.ts": "bad"})))
	out := httptest.NewRecorder()
	a.handleSourceImport(out, req)
	if out.Code != 400 {
		t.Fatalf("status %d", out.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "keep.txt")); string(b) != "keep" {
		t.Fatal("invalid import changed app")
	}
}
func TestAppConfigReplacementAndRevision(t *testing.T) {
	current := []string{"PATH=/usr/bin", "OLD_SECRET=creator", "KEEP=previous", "RUNTIMED_APP_ENV_KEYS=OLD_SECRET,KEEP", "RUNTIMED_APP_CONFIG_REVISION=old", "RUNTIMED_HTTP_ADDR=:3031"}
	next := replacementAppEnv(current, &runtime.AppConfigRequest{Env: map[string]string{"KEEP": "new"}, Revision: "app:2"})
	m := envMap(next)
	if _, ok := m["OLD_SECRET"]; ok {
		t.Fatal("deleted config leaked into restart")
	}
	if m["KEEP"] != "new" || m["PATH"] != "/usr/bin" || m["RUNTIMED_APP_CONFIG_REVISION"] != "app:2" {
		t.Fatalf("replacement %+v", m)
	}
	restart := make(chan struct{}, 1)
	a := &app{appConfigRevision: "old", requestRestart: func() { restart <- struct{}{} }}
	out := httptest.NewRecorder()
	a.handleAppConfig(out, httptest.NewRequest("POST", "/config", strings.NewReader(`{"env":{"TOKEN":"sensitive"},"revision":"new"}`)))
	if out.Code != 202 || strings.Contains(out.Body.String(), "sensitive") {
		t.Fatalf("config %d %s", out.Code, out.Body)
	}
	if a.status().AppConfigRevision != "old" {
		t.Fatal("reported unapplied revision before exec")
	}
	select {
	case <-restart:
	case <-time.After(time.Second):
		t.Fatal("config restart missing")
	}
	a = &app{appConfigRevision: "new", requestRestart: func() { t.Error("idempotent config restarted") }}
	out = httptest.NewRecorder()
	a.handleAppConfig(out, httptest.NewRequest("POST", "/config", strings.NewReader(`{"env":{},"revision":"new"}`)))
	if out.Code != 204 {
		t.Fatal("same revision not idempotent")
	}
	for _, key := range []string{"RUNTIMED_HTTP_TOKEN", "PATH", "LD_PRELOAD", "NODE_OPTIONS"} {
		if runtime.ValidAppConfigKey(key) {
			t.Fatalf("reserved key %s accepted", key)
		}
	}
}

func TestImportReusesOnlyMatchingFreshTemplateDependencies(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-lockfiles", true: "different-lockfile"}[changed], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "app")
			os.Mkdir(root, 0755)
			os.Mkdir(filepath.Join(root, "node_modules"), 0755)
			os.WriteFile(filepath.Join(root, "node_modules", "fresh-marker"), []byte("fresh template only"), 0644)
			os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{}}`), 0644)
			os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), []byte("lock original"), 0644)
			lock := "lock original"
			if changed {
				lock = "lock changed"
			}
			data := importTestZip(t, map[string]string{"package.json": `{"dependencies":{}}`, "pnpm-lock.yaml": lock, "src/main.ts": "new source"})
			err := replaceSource(root, data)
			if changed {
				if !errors.Is(err, errDependencyMismatch) {
					t.Fatalf("mismatch accepted: %v", err)
				}
				if _, e := os.Stat(filepath.Join(root, "src/main.ts")); !os.IsNotExist(e) {
					t.Fatal("mismatch changed source")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(root, "node_modules", "fresh-marker")); string(b) != "fresh template only" {
				t.Fatal("fresh dependencies lost")
			}
		})
	}
}
