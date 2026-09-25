package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func TestLatestEvidenceRequiresAllThreeStores(t *testing.T) {
	latest := marker{"fixture", "latest", "new"}
	data := map[string]any{"matches": true, "app": latest, "home": latest, "database": latest}
	b, _ := json.Marshal(data)
	if validateEvidence(b, latest) != nil {
		t.Fatal("exact evidence rejected")
	}
	for _, field := range []string{"app", "home", "database"} {
		copy := map[string]any{}
		for k, v := range data {
			copy[k] = v
		}
		copy[field] = marker{"fixture", "baseline", "old"}
		b, _ = json.Marshal(copy)
		if validateEvidence(b, latest) == nil {
			t.Fatalf("stale %s passed", field)
		}
	}
	if validateEvidence([]byte(`{"matches":true}`), latest) == nil {
		t.Fatal("health-only passed")
	}
}
func TestEvidenceCreationRefusesOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "escrow.private.json")
	if e := durable(p, []byte("original"), true); e != nil {
		t.Fatal(e)
	}
	if e := durable(p, []byte("replacement"), true); e == nil {
		t.Fatal("overwrote prior escrow")
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != "original" {
		t.Fatal("prior escrow changed")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("escrow not private")
	}
}
func TestCleanupRequiresExactOwnershipThenGET404(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "foreign"}[foreign], func(t *testing.T) {
			var deleted atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deleted.Store(true)
					w.WriteHeader(204)
					return
				}
				if deleted.Load() {
					w.WriteHeader(404)
					return
				}
				owner := "synthetic"
				if foreign {
					owner = "other"
				}
				json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-crash-test", TemplateID: template, CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"operator-crash-fixture": owner}})
			}))
			defer server.Close()
			api, e := cube.New(cube.Config{APIURL: server.URL, APIKey: "synthetic-only"})
			if e != nil {
				t.Fatal(e)
			}
			c := &coordinator{cube: api, saved: escrow{Fixture: "synthetic", Guest: &cube.Sandbox{SandboxID: "vm-crash-test"}}}
			if got := c.cleanup(); got == foreign {
				t.Fatalf("cleanup=%v foreign=%v", got, foreign)
			}
			if deleted.Load() == foreign {
				t.Fatal("cleanup deleted foreign guest or missed owned guest")
			}
		})
	}
}
func TestInventoryRequiresOneExactZero(t *testing.T) {
	if !emptyInventory("SANDBOX_COUNT    0\n") {
		t.Fatal("empty rejected")
	}
	for _, s := range []string{"", "SANDBOX_COUNT    1", "SANDBOX_COUNT    0\nSANDBOX_COUNT    1", "SANDBOX_COUNT    00"} {
		if emptyInventory(s) {
			t.Fatal("nonempty/ambiguous accepted")
		}
	}
}

func TestMachineIdentityRequiresExactCanonicalIdentity(t *testing.T) {
	good := "0123456789abcdef0123456789abcdef"
	if !validMachineIdentity(good, good) {
		t.Fatal("canonical identity rejected")
	}
	for _, value := range []string{"", "00000000000000000000000000000000", "0123456789abcdef0123456789abcdeF", good + "\n", "01234567-89ab-cdef-0123-456789abcdef", "1123456789abcdef0123456789abcdef"} {
		if validMachineIdentity(value, good) {
			t.Fatal("invalid or changed machine identity accepted")
		}
	}
}

func TestPrivateProbeArchivePreservesSecretsModesAndLinks(t *testing.T) {
	var source bytes.Buffer
	w := zip.NewWriter(&source)
	inputs := []struct {
		name, body string
		mode       os.FileMode
	}{
		{"sandbox.yaml", "workers:\n  - name: postgres\n    command: node pg.mjs\nbuild:\n  command: true\n", 0644},
		{"private.env", "OWNER_SECRET=preserve\n", 0600},
		{"tool.sh", "#!/bin/sh\nexit 0\n", 0755},
		{"tool-link", "tool.sh", os.ModeSymlink | 0777},
		{"config/", "", os.ModeDir | 0750},
	}
	for _, input := range inputs {
		h := zip.FileHeader{Name: input.name, Method: zip.Deflate, Comment: "original-header"}
		h.SetMode(input.mode)
		out, e := w.CreateHeader(&h)
		if e != nil {
			t.Fatal(e)
		}
		out.Write([]byte(input.body))
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if rt.PublishedSourcePath("crash-fixture-token") {
		t.Fatal("regression requires capability to be excluded from publication")
	}
	cap := strings.Repeat("a", 64)
	var patched bytes.Buffer
	if e := buildProbeArchive(bytes.NewReader(source.Bytes()), int64(source.Len()), &patched, []byte("// private fixture probe\n"), "fixture", cap); e != nil {
		t.Fatal(e)
	}
	if e := rt.ValidatePrivateWorkspaceFileInterpreters(bytes.NewReader(patched.Bytes()), int64(patched.Len())); e != nil {
		t.Fatal("private archive rejected", e)
	}
	z, e := zip.NewReader(bytes.NewReader(patched.Bytes()), int64(patched.Len()))
	if e != nil {
		t.Fatal(e)
	}
	entries := map[string]*zip.File{}
	for _, f := range z.File {
		entries[f.Name] = f
	}
	for _, input := range inputs {
		if input.name == "sandbox.yaml" {
			continue
		}
		f := entries[input.name]
		if f == nil || f.Mode() != input.mode || f.Comment != "original-header" {
			t.Fatalf("lost original header or mode for %s", input.name)
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil || string(b) != input.body {
			t.Fatal("original private content changed")
		}
	}
	for _, name := range []string{"sandbox.yaml", "probe.mjs", "config/crash-fixture.json", "crash-fixture-token"} {
		f := entries[name]
		want := os.FileMode(0644)
		if name == "crash-fixture-token" {
			want = 0600
		}
		if f == nil || f.Mode() != want {
			t.Fatalf("wrong private fixture mode %s", name)
		}
	}
	r, e := entries["crash-fixture-token"].Open()
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(r)
	r.Close()
	if e != nil || string(b) != cap {
		t.Fatal("private capability lost")
	}
}

func TestProbeInstallQuiescesAndUsesOnlyPrivateWorkspaceEndpoints(t *testing.T) {
	var original bytes.Buffer
	w := zip.NewWriter(&original)
	h := zip.FileHeader{Name: "sandbox.yaml"}
	h.SetMode(0644)
	out, e := w.CreateHeader(&h)
	if e != nil {
		t.Fatal(e)
	}
	out.Write([]byte("workers:\n  - name: postgres\n    command: node pg.mjs\nbuild:\n  command: true\n"))
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var calls []string
	quiesced := false
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("supervisor authentication missing")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /workspace/quiesce":
			quiesced = true
			w.Write([]byte(`{"quiesced":true}`))
		case "GET /export/private-workspace-v2":
			if !quiesced {
				t.Error("export before quiescence")
			}
			w.Write(original.Bytes())
		case "GET /status":
			w.Write([]byte(`{"runtimed":{"booted_at":"2026-09-25T00:00:00Z"}}`))
		case "PUT /import/private-workspace-v2":
			if !quiesced {
				t.Error("import before quiescence")
			}
			data, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			if e = rt.ValidatePrivateWorkspaceFileInterpreters(bytes.NewReader(data), int64(len(data))); e != nil {
				t.Error("invalid private import", e)
			}
			z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			found := false
			for _, f := range z.File {
				if f.Name == "crash-fixture-token" {
					found = true
					if f.Mode().Perm() != 0600 {
						t.Error("private capability not0600")
					}
				}
			}
			if !found {
				t.Error("private capability absent")
			}
			w.Write([]byte(`{"imported":true,"restarting":true}`))
		default:
			t.Error("unexpected public/source endpoint", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client, e := rt.NewRemoteClient(rt.RemoteConfig{BaseURL: server.URL, Token: token})
	if e != nil {
		t.Fatal(e)
	}
	stage := t.TempDir()
	if e = os.WriteFile(filepath.Join(stage, "probe.mjs"), []byte("// fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	c := &coordinator{ctx: context.Background(), stage: stage, guest: client, saved: escrow{Fixture: "owned", Capability: token}}
	if got := c.importPrivateProbe(); !got.Equal(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("pre-import boot identity lost")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST /workspace/quiesce", "GET /export/private-workspace-v2", "GET /status", "PUT /import/private-workspace-v2"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("wrong private installation sequence: %v", calls)
	}
	for _, name := range []string{"probe-install-before.private.zip", "probe-install.private.zip"} {
		st, e := os.Stat(filepath.Join(stage, name))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("private local archive permissions")
		}
	}
}
