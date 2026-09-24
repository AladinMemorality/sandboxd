package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeSourceRestorePreservesOwnerVMAndRejectsUnsafeReplacement(t *testing.T) {
	for _, scenario := range []string{"success", "import failure", "active task", "guest active task", "template mismatch", "preview starting", "invalid archive", "other owner"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var imports, destructive atomic.Int32
			revision := ""
			boot := time.Now().UTC().Add(-time.Hour)
			s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/status":
					status := map[string]any{"runtimed": map[string]any{"booted_at": boot}, "app_config_revision": revision, "preview": map[string]any{"status": "ready"}}
					if scenario == "guest active task" {
						status["active_task"] = map[string]string{"id": "guest-running-task"}
					}
					if scenario == "preview starting" {
						status["preview"] = map[string]string{"status": "starting"}
					}
					json.NewEncoder(w).Encode(status)
				case "/config":
					var cfg runtime.AppConfigRequest
					if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
						t.Error(err)
					}
					revision = cfg.Revision
					w.WriteHeader(202)
				case "/import/source":
					imports.Add(1)
					if r.Host != "3031-vm-tasks.cube.test" {
						t.Error("restore changed guest identity")
					}
					if scenario == "import failure" {
						http.Error(w, "fixture rejects import", 409)
						return
					}
					data, _ := io.ReadAll(r.Body)
					if _, err := runtime.SanitizeSourceArchive(data); err != nil {
						t.Error(err)
					}
					boot = boot.Add(time.Minute)
					io.WriteString(w, `{"imported":true,"restarting":true}`)
				default:
					t.Errorf("unexpected guest operation %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/sandboxes/vm-tasks/connect" {
					destructive.Add(1)
					http.Error(w, "restore must not create or delete a VM", 500)
					return
				}
				io.WriteString(w, `{"sandboxID":"vm-tasks","templateID":"tpl-tasks","state":"running"}`)
			}))
			t.Cleanup(management.Close)
			var err error
			s.Cube, err = cube.New(cube.Config{APIURL: management.URL, APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			s.CubeTemplates = map[string]string{"react-pro": "tpl-tasks"}
			if scenario == "template mismatch" {
				s.CubeTemplates["react-pro"] = "tpl-different"
			}
			s.LibraryRoot = t.TempDir()
			before, err := s.Store.Get(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			bindingBefore, err := s.Store.GetRuntimeBinding(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			snap := &store.Snapshot{ID: newULID(), Name: "source version", OwnerToken: cfgTenant, Status: "ready", Format: cubeSourceFormat, BaseImage: cubeSourcePresetPrefix + "react-pro"}
			snap.ImagePath = filepath.Join(s.LibraryRoot, snap.ID+".cube.zip")
			data := publishedZip(t, map[string]string{"package.json": "{}", "index.html": "restored source"})
			if scenario == "invalid archive" {
				data = []byte("invalid")
			}
			if err = os.WriteFile(snap.ImagePath, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = s.Store.CreateSnapshot(context.Background(), snap); err != nil {
				t.Fatal(err)
			}
			if scenario == "active task" {
				if err = s.Store.CreateTask(context.Background(), &store.Task{TaskID: newULID(), SandboxID: id, Agent: "fixture", Prompt: "running"}); err != nil {
					t.Fatal(err)
				}
			}
			owner := cfgTenant
			if scenario == "other owner" {
				owner = "other-owner"
			}
			out := cubeRequest(s, "POST", "/v1/apps/"+before.AppID.String+"/restore", `{"snapshot_id":"`+snap.ID+`"}`, owner)
			want := map[string]int{"success": 201, "import failure": 502, "active task": 409, "guest active task": 409, "template mismatch": 409, "preview starting": 201, "invalid archive": 422, "other owner": 404}[scenario]
			if out.Code != want {
				t.Fatalf("restore HTTP%d, want%d: %s", out.Code, want, out.Body.String())
			}
			after, err := s.Store.CurrentSandboxForApp(context.Background(), before.AppID.String)
			if err != nil || after.ID != id {
				t.Fatalf("owner sandbox replaced: %v", err)
			}
			bindingAfter, err := s.Store.GetRuntimeBinding(context.Background(), id)
			if err != nil || bindingAfter.RuntimeID != bindingBefore.RuntimeID || !bytes.Equal(bindingAfter.TokenCiphertext, bindingBefore.TokenCiphertext) || !bytes.Equal(bindingAfter.TokenNonce, bindingBefore.TokenNonce) {
				t.Fatalf("owner VM or credentials replaced: %v", err)
			}
			if destructive.Load() != 0 {
				t.Fatal("attempted destructive VM replacement")
			}
			wantImports := int32(0)
			if scenario == "success" || scenario == "import failure" || scenario == "preview starting" {
				wantImports = 1
			}
			if imports.Load() != wantImports {
				t.Fatalf("imports=%d, want%d", imports.Load(), wantImports)
			}
			if scenario == "preview starting" {
				var response v1Sandbox
				if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil || response.Preview.Status != "starting" {
					t.Fatal("restore falsely reported application readiness")
				}
			}
		})
	}
}
