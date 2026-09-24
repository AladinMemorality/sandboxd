package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const reloadManifestSource = "version: 1\nweb:\n  command: node server.mjs\n  port: 3000\nworkers:\n  - name: postgres\n    command: node /opt/services/postgres/worker.mjs\n    restart_after_task: false\n"

type manifestReloadState struct {
	mu              sync.Mutex
	source          string
	revision        string
	applied         []runtime.AppConfigRequest
	reads, connects int
	active          bool
}

func manifestReloadFixture(t *testing.T) (*Server, string, *manifestReloadState) {
	t.Helper()
	state := &manifestReloadState{source: reloadManifestSource}
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("supervisor authentication missing")
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/files/content":
			if r.URL.Query().Get("path") != "sandbox.yaml" {
				t.Errorf("unexpected file read %s", r.URL.Query().Get("path"))
			}
			state.reads++
			fmt.Fprint(w, state.source)
		case r.Method == "GET" && r.URL.Path == "/status":
			status := runtime.Status{AppConfigRevision: state.revision}
			if state.active {
				status.ActiveTask = &runtime.ActiveTask{ID: "synthetic-active-task"}
			}
			_ = json.NewEncoder(w).Encode(status)
		case r.Method == "POST" && r.URL.Path == "/config":
			var config runtime.AppConfigRequest
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				t.Error(err)
			}
			state.applied = append(state.applied, config)
			state.revision = config.Revision
			w.WriteHeader(202)
		default:
			// In particular: never import source/home, recreate a workspace,
			// or run arbitrary exec as part of activating the existing manifest.
			t.Errorf("unexpected guest operation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/sandboxes/vm-tasks/connect" {
			t.Errorf("must not replace, snapshot or delete VM: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		state.mu.Lock()
		state.connects++
		state.mu.Unlock()
		fmt.Fprint(w, `{"sandboxID":"vm-tasks","templateID":"tpl-tasks","state":"running"}`)
	}))
	t.Cleanup(provider.Close)
	var err error
	s.Cube, err = cube.New(cube.Config{APIURL: provider.URL, APIKey: "synthetic-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	return s, id, state
}

func pendingManifestConfig(t *testing.T, s *Server, id string) []*store.AppConfig {
	t.Helper()
	sb, err := s.Store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"key":"APP_COLOR","value":"blue","access_policy":"runtime_access"}`,
		`{"key":"DATABASE_KEY","value":"synthetic-db-secret","sensitive":true,"access_policy":"both"}`,
		`{"key":"PRIVATE_AGENT_KEY","value":"synthetic-agent-secret","sensitive":true,"access_policy":"agent_access"}`,
		`{"key":"PRIVATE_CONTROL_KEY","value":"synthetic-control-secret","sensitive":true,"access_policy":"control_plane_only"}`,
	} {
		w := cubeRequest(s, "POST", "/v1/apps/"+sb.AppID.String+"/config", body, cfgTenant)
		if w.Code != 201 {
			t.Fatalf("save config %d %s", w.Code, w.Body.String())
		}
	}
	rows, err := s.Store.ListAppConfig(context.Background(), sb.AppID.String)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestCubeManifestReloadPreservesIdentityConfigAndRetries(t *testing.T) {
	s, id, state := manifestReloadFixture(t)
	beforeConfig := pendingManifestConfig(t, s, id)
	ctx := context.Background()
	before, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/sandboxes/" + id + "/recreate"
	for i := 0; i < 2; i++ {
		w := cubeRequest(s, "POST", path, `{"reload_manifest":true}`, cfgTenant)
		if w.Code != 200 {
			t.Fatalf("reload %d: %d %s", i, w.Code, w.Body.String())
		}
		var got struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.ID != id {
			t.Fatalf("stable API id changed: %s", w.Body.String())
		}
	}
	state.mu.Lock()
	first := append([]runtime.AppConfigRequest(nil), state.applied...)
	state.mu.Unlock()
	if len(first) != 1 {
		t.Fatalf("initial reload and identical retry must apply once, got %d", len(first))
	}
	expectedRevision := fmt.Sprintf("%s:%d:manifest:%x", id, before.ConfigRevision, sha256.Sum256([]byte(reloadManifestSource)))
	if first[0].Revision != expectedRevision {
		t.Fatalf("revision does not bind exact source: %q", first[0].Revision)
	}
	expectedEnv := map[string]string{"APP_COLOR": "blue", "DATABASE_KEY": "synthetic-db-secret"}
	if !reflect.DeepEqual(first[0].Env, expectedEnv) {
		t.Fatal("runtime-visible config not preserved exactly or private config leaked")
	}

	// A changed source is activated once even with the same stored config revision.
	state.mu.Lock()
	state.source += "# changed worker configuration\n"
	state.mu.Unlock()
	for i := 0; i < 2; i++ {
		w := cubeRequest(s, "POST", path, `{"reload_manifest":true}`, cfgTenant)
		if w.Code != 200 {
			t.Fatalf("changed reload %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	state.mu.Lock()
	if len(state.applied) != 2 {
		t.Errorf("changed source and retry applied %d times, want 2 total", len(state.applied))
	}
	if len(state.applied) >= 2 && (state.applied[1].Revision == first[0].Revision || !reflect.DeepEqual(state.applied[1].Env, expectedEnv)) {
		t.Error("changed manifest lost revision/config identity")
	}
	reads := state.reads
	state.mu.Unlock()
	// Ordinary recreate remains a fast idempotent config operation; it does not
	// reread the manifest or restart an already configured supervisor.
	for _, body := range []string{"", "{}", `{"reload_manifest":false}`} {
		w := cubeRequest(s, "POST", path, body, cfgTenant)
		if w.Code != 200 {
			t.Fatalf("ordinary recreate %d %s", w.Code, w.Body.String())
		}
	}
	state.mu.Lock()
	if state.reads != reads || len(state.applied) != 2 {
		t.Error("ordinary recreate unexpectedly read/applied manifest")
	}
	state.mu.Unlock()
	after, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	before.ConfigAppliedRevision = before.ConfigRevision
	if !reflect.DeepEqual(after, before) {
		t.Fatal("VM/template/credentials/config revision identity changed")
	}
	afterSB, err := s.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if afterSB.ID != sb.ID || afterSB.AppID != sb.AppID || afterSB.WorkspaceImg != sb.WorkspaceImg || afterSB.WorkspaceMnt != sb.WorkspaceMnt || afterSB.CreatedAt != sb.CreatedAt {
		t.Fatal("persistent sandbox/workspace identity changed")
	}
	afterConfig, err := s.Store.ListAppConfig(ctx, sb.AppID.String)
	if err != nil || !reflect.DeepEqual(afterConfig, beforeConfig) {
		t.Fatal("stored config values, ciphertext, policies or metadata changed")
	}
}

func TestCubeManifestReloadDeniesMalformedBodyBeforeWake(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `null`, `{"reload_manifest":null}`, `{"reload_manifest":"true"}`, `{"reload_manifest":1}`, `{"unknown":true}`, `{} {}`, `{"reload_manifest":true}garbage`, `{"reload_manifest":true,"padding":"` + strings.Repeat("x", 1100) + `"}`} {
		t.Run(fmt.Sprintf("body_%x", sha256.Sum256([]byte(body))), func(t *testing.T) {
			s, id, state := manifestReloadFixture(t)
			w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/recreate", body, cfgTenant)
			if w.Code != 400 {
				t.Fatalf("malformed body returned %d %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.connects != 0 || state.reads != 0 || len(state.applied) != 0 {
				t.Fatal("invalid request touched guest")
			}
		})
	}
}

func TestCubeManifestReloadDeniesInvalidManifestBeforePendingConfigApply(t *testing.T) {
	for name, source := range map[string]string{
		"bad_yaml": "version: [", "unsupported_version": "version: 99\n", "unknown_service": "version: 1\nservices: {}\n",
		"wrong_port":   strings.Replace(reloadManifestSource, "port: 3000", "port: 8080", 1),
		"invalid_port": strings.Replace(reloadManifestSource, "port: 3000", "port: 70000", 1),
		"empty_worker": "version: 1\nworkers:\n  - name: postgres\n",
		"too_large":    reloadManifestSource + "#" + strings.Repeat("x", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			s, id, state := manifestReloadFixture(t)
			pendingManifestConfig(t, s, id)
			state.source = source
			before, err := s.Store.GetRuntimeBinding(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/recreate", `{"reload_manifest":true}`, cfgTenant)
			if w.Code != 422 {
				t.Fatalf("invalid manifest returned %d %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			applies := len(state.applied)
			state.mu.Unlock()
			if applies != 0 {
				t.Fatal("invalid manifest activated through pending config before validation")
			}
			after, err := s.Store.GetRuntimeBinding(context.Background(), id)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatal("invalid manifest changed runtime binding/config acknowledgement")
			}
		})
	}
}

func TestCubeManifestReloadDeniesForeignOwnerAndActiveTasks(t *testing.T) {
	for _, mode := range []string{"foreign_owner", "stored_task", "guest_task"} {
		t.Run(mode, func(t *testing.T) {
			s, id, state := manifestReloadFixture(t)
			pendingManifestConfig(t, s, id)
			owner, code := cfgTenant, 409
			if mode == "foreign_owner" {
				owner, code = "foreign-owner", 404
			}
			if mode == "guest_task" {
				state.active = true
			}
			if mode == "stored_task" {
				if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: "01M2QHT40D9S9W5MFNN32F1DXW", SandboxID: id, Agent: "opencode", Prompt: "synthetic active task"}); err != nil {
					t.Fatal(err)
				}
			}
			w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/recreate", `{"reload_manifest":true}`, owner)
			if w.Code != code {
				t.Fatalf("denied %s returned %d %s", mode, w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if len(state.applied) != 0 || state.reads != 0 {
				t.Fatal("denied reload read source or applied configuration")
			}
			if mode != "guest_task" && state.connects != 0 {
				t.Fatal("denied request woke guest")
			}
		})
	}
}
