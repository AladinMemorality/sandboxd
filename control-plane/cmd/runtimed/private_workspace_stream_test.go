package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivateWorkspaceFileRoutesRequireAuthAndQuiescence(t *testing.T) {
	a := &app{}
	token := strings.Repeat("ab", 32)
	handler := authenticatedControl(token, a.controlHandler())
	for _, method := range []string{"GET", "PUT"} {
		path := "/export/private-workspace-v2"
		if method == "PUT" {
			path = "/import/private-workspace-v2"
		}
		r := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unauthenticated", w.Code)
		}
		r = httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 409 {
			t.Fatal("unquiesced", w.Code)
		}
	}
}

func TestPrivateWorkspaceFileGuestRoundtripAndRestartFence(t *testing.T) {
	if os.Geteuid() != 1000 || os.Getenv("CUBE_WORKSPACE_GUEST_TEST") != "1" || os.Getenv("RUNTIMED_CUBE_GUEST") != "1" {
		t.Skip("dedicated disposable UID1000 guest fixture required")
	}
	home := "/home/sandbox"
	appDir := filepath.Join(home, "workspace/app")
	runtimeDir := filepath.Join(home, ".runtimed")
	for _, dir := range []string{appDir, runtimeDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(appDir, ".env"), []byte("owner state"), 0600)
	restarted := make(chan struct{}, 1)
	a := &app{appDir: appDir, runtimeDir: runtimeDir, requestRestart: func() { restarted <- struct{}{} }}
	token := strings.Repeat("ab", 32)
	handler := authenticatedControl(token, a.controlHandler())
	call := func(method, path string, data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/workspace/quiesce", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	exported := call("GET", "/export/private-workspace-v2", nil)
	if exported.Code != 200 {
		t.Fatal(exported.Code, exported.Body)
	}
	os.WriteFile(filepath.Join(appDir, ".env"), []byte("changed"), 0600)
	if w := call("PUT", "/import/private-workspace-v2", []byte("invalid")); w.Code != 422 {
		t.Fatal(w.Code, w.Body)
	}
	if got, _ := os.ReadFile(filepath.Join(appDir, ".env")); string(got) != "changed" {
		t.Fatal("invalid import mutated root")
	}
	if w := call("PUT", "/import/private-workspace-v2", exported.Body.Bytes()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if got, _ := os.ReadFile(filepath.Join(appDir, ".env")); string(got) != "owner state" {
		t.Fatal("owner state lost")
	}
	if w := call("PUT", "/files?path=blocked", []byte("no")); w.Code != 409 {
		t.Fatal("mutation fence lost")
	}
	if w := call("POST", "/workspace/resume", nil); w.Code != 409 {
		t.Fatal("restart fence lost", w.Code)
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("restart not requested")
	}
	a.taskMu.Lock()
	quiesced, pending := a.workspaceQuiesced, a.restartPending
	a.taskMu.Unlock()
	if !quiesced || !pending {
		t.Fatal("import silently resumed")
	}
}
