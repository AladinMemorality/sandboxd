package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func TestPrivateHomeRoutesRequireAuthAndQuiescence(t *testing.T) {
	a := &app{}
	token := strings.Repeat("ab", 32)
	handler := authenticatedControl(token, a.controlHandler())
	for _, method := range []string{"POST", "PUT"} {
		path := "/export/private-home"
		if method == "PUT" {
			path = "/import/private-home"
		}
		r := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("home unauthenticated", w.Code)
		}
		r = httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 409 {
			t.Fatal("home unquiesced", w.Code)
		}
	}
}
func TestPrivateHomeGuestFileBackedRoundtrip(t *testing.T) {
	if os.Geteuid() != 1000 || os.Getenv("CUBE_HOME_GUEST_TEST") != "1" || os.Getenv("RUNTIMED_CUBE_GUEST") != "1" {
		t.Skip("dedicated disposable UID1000 guest fixture required")
	}
	home := "/home/sandbox"
	appDir := filepath.Join(home, "workspace/app")
	runtimeDir := filepath.Join(home, ".runtimed")
	for _, dir := range []string{appDir, runtimeDir, filepath.Join(home, "workspace/data")} {
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(home, "workspace/data/owner.txt"), []byte("owner state"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(runtimeDir, "control-identity"), []byte("new guest identity"), 0600); e != nil {
		t.Fatal(e)
	}
	a := &app{appDir: appDir, runtimeDir: runtimeDir}
	token := strings.Repeat("ab", 32)
	handler := authenticatedControl(token, a.controlHandler())
	m := runtime.HomeManifest{Version: 1, Entries: []runtime.HomeManifestEntry{{Path: ".runtimed", Disposition: "separate"}, {Path: "workspace/app", Disposition: "separate"}, {Path: "workspace/data", Disposition: "preserve"}}}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path string, data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token)
		if method == "PUT" {
			r.Header.Set("X-Home-Manifest", base64.RawURLEncoding.EncodeToString(raw))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/workspace/quiesce", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	exported := call("POST", "/export/private-home", raw)
	if exported.Code != 200 {
		t.Fatal(exported.Code, exported.Body)
	}
	if e = os.WriteFile(filepath.Join(home, "workspace/data/owner.txt"), []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if w := call("PUT", "/import/private-home", exported.Body.Bytes()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if got, e := os.ReadFile(filepath.Join(home, "workspace/data/owner.txt")); e != nil || string(got) != "owner state" {
		t.Fatal("owner state not restored", e)
	}
	if got, e := os.ReadFile(filepath.Join(runtimeDir, "control-identity")); e != nil || string(got) != "new guest identity" {
		t.Fatal("control identity lost", e)
	}
	if !a.workspaceQuiesced {
		t.Fatal("home import silently resumed")
	}
	if w := call("PUT", "/files?path=blocked", []byte("no")); w.Code != 409 {
		t.Fatal("mutation fence lost")
	}
	if w := call("POST", "/workspace/resume", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}
