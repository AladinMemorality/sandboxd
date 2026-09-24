package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
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
	for _, method := range []string{"POST", "PUT", "POST-v2", "PUT-v2"} {
		suffix := ""
		if strings.HasSuffix(method, "-v2") {
			suffix = "-v2"
			method = strings.TrimSuffix(method, "-v2")
		}
		path := "/export/private-home"
		if method == "PUT" {
			path = "/import/private-home"
		}
		path += suffix
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
	m.Version = 2
	linkName := "workspace/data/bin/python3"
	if e = os.MkdirAll(filepath.Join(home, "workspace/data/bin"), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("/usr/bin/python3.13", filepath.Join(home, linkName)); e != nil {
		t.Fatal(e)
	}
	m.Links = []runtime.HomeLinkContract{{Path: linkName, Target: "/usr/bin/python3.13", Kind: "python-interpreter"}}
	literalName := `chromelibs/usr/lib/systemd/system/system-systemd\x2dcryptsetup.slice`
	if e = os.MkdirAll(filepath.Dir(filepath.Join(home, literalName)), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, literalName), []byte("literal package data"), 0644); e != nil {
		t.Fatal(e)
	}
	m.Entries = append(m.Entries, runtime.HomeManifestEntry{Path: "chromelibs", Disposition: "preserve"})
	m.LiteralPaths = []string{literalName}
	raw, e = runtime.CanonicalHomeManifest(m)
	if e != nil {
		t.Fatal(e)
	}
	exported = call("POST", "/export/private-home-v2", raw)
	if exported.Code != 200 {
		t.Fatal(exported.Code, exported.Body)
	}
	var frame bytes.Buffer
	binary.Write(&frame, binary.BigEndian, uint32(len(raw)))
	frame.Write(raw)
	frame.Write(exported.Body.Bytes())
	if w := call("PUT", "/import/private-home-v2", frame.Bytes()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if got, e := os.Readlink(filepath.Join(home, linkName)); e != nil || got != "/usr/bin/python3.13" {
		t.Fatal("guest link changed", e)
	}
	if got, e := os.ReadFile(filepath.Join(home, literalName)); e != nil || string(got) != "literal package data" {
		t.Fatal("guest literal filename changed", e)
	}
	for _, data := range [][]byte{{0, 0, 0}, {0, 0, 255, 255}, {0, 0, 0, 1, 123}, {0, 0, 0, 0}} {
		if w := call("PUT", "/import/private-home-v2", data); w.Code != 400 {
			t.Fatal("bad frame accepted", w.Code, w.Body)
		}
	}
	if w := call("POST", "/export/private-home", raw); w.Code != 400 {
		t.Fatal("version downgrade accepted", w.Code, w.Body)
	}
	if w := call("POST", "/workspace/resume", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}
