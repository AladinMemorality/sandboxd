package main

import (
	"bytes"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateWorkspaceRequiresQuiescenceAndFencesMutation(t *testing.T) {
	if os.Geteuid() != 1000 {
		t.Skip("guest quiescence integration requires isolated UID1000")
	}
	base, e := os.MkdirTemp("/home/sandbox/workspace", "quiesce-test-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(base)
	root := filepath.Join(base, "app")
	os.Mkdir(root, 0755)
	os.WriteFile(filepath.Join(root, "owner.txt"), []byte("data"), 0644)
	a := &app{appDir: root, runtimeDir: t.TempDir(), requestRestart: func() {}}
	h := a.controlHandler()
	call := func(method, path string, body []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(body)))
		return w
	}
	if w := call("GET", "/export/private-workspace", nil); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/workspace/quiesce", nil); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := call("PUT", "/files?path=owner.txt", []byte("overwrite")); w.Code != 409 {
		t.Fatal("write not fenced")
	}
	archive := call("GET", "/export/private-workspace", nil)
	if archive.Code != 200 {
		t.Fatal(archive.Code)
	}
	if e := runtime.ValidatePrivateWorkspaceArchive(archive.Body.Bytes()); e != nil {
		t.Fatal(e)
	}
	if w := call("PUT", "/import/private-workspace", archive.Body.Bytes()); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, e := os.Stat(filepath.Join(a.runtimeDir, "workspace-quiesced")); e != nil {
		t.Fatal("import lost restart fence")
	}
	a.restartPending = false // Simulate the new supervisor boot before explicit resume.
	if w := call("POST", "/workspace/resume", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if a.workspaceQuiesced || a.restartPending {
		t.Fatal("resume did not release fence")
	}
}

func TestWorkspaceFenceDoesNotBlockStatusBehindPreparation(t *testing.T) {
	a := &app{workspaceQuiesced: true}
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		a.controlHandler().ServeHTTP(w, httptest.NewRequest("GET", "/status", nil))
		done <- w.Code
	}()
	select {
	case status := <-done:
		if status != 200 {
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		t.Fatal("status held behind dependency preparation write fence")
	}
}
