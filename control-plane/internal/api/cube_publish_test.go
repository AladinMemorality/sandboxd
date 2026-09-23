package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func publishedZip(t *testing.T, files map[string]string) []byte {
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
func TestCubePublishForkFreshCredentialsAndNoPrivateSource(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "cube-source"
		if legacy {
			name = "legacy-source-global"
		}
		t.Run(name, func(t *testing.T) { testCubePublishForkFreshCredentialsAndNoPrivateSource(t, legacy) })
	}
}

func testCubePublishForkFreshCredentialsAndNoPrivateSource(t *testing.T, legacy bool) {
	s, _ := newConfigTestServer(t)
	appID := newULID()
	if err := s.Store.CreateApp(context.Background(), &store.App{ID: appID, OwnerToken: cfgTenant, Name: "Source", RuntimePreset: sql.NullString{String: "react-vite", Valid: true}}); err != nil {
		t.Fatal(err)
	}
	s.LibraryRoot = t.TempDir()
	s.CubeTemplates = map[string]string{"react-vite": "tpl-safe"}
	s.CubeDomain = "cube.test"
	secretEnc, secretNonce, err := s.Secrets.Seal([]byte("CREATOR_CONFIG_SECRET"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.CreateAppConfig(context.Background(), &store.AppConfig{ID: "creator-config", AppID: appID, Key: "PRIVATE_API_KEY", ValueCiphertext: secretEnc, ValueNonce: secretNonce, Sensitive: true, AccessPolicy: "runtime_access"}); err != nil {
		t.Fatal(err)
	}

	sourceToken := strings.Repeat("ab", 32)
	newToken := ""
	imported := false
	sourceRevision := ""
	before := time.Now().UTC().Add(-time.Hour)
	raw := publishedZip(t, map[string]string{"package.json": "{}", "src/main.ts": "published code", ".env.local": "SECRET_ENV", "local.db": "SECRET_DB", "bench-state.json": "SECRET_STATE", "credentials.json": "SECRET_CREDS", "secrets.json": "SECRET_SECRETS", ".runtimed/key": "SECRET_RUNTIME"})
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedToken := sourceToken
		if strings.HasPrefix(r.Host, "3031-vm-fork.") {
			expectedToken = newToken
		}
		if r.Header.Get("Authorization") != "Bearer "+expectedToken {
			t.Errorf("wrong per-guest identity")
		}
		switch r.URL.Path {
		case "/status":
			boot := before
			if imported && expectedToken == newToken {
				boot = before.Add(time.Minute)
			}
			revision := ""
			if expectedToken == sourceToken {
				revision = sourceRevision
			}
			json.NewEncoder(w).Encode(map[string]any{"runtimed": map[string]any{"booted_at": boot}, "app_config_revision": revision})
		case "/config":
			var config struct {
				Revision string `json:"revision"`
			}
			json.NewDecoder(r.Body).Decode(&config)
			sourceRevision = config.Revision
			w.WriteHeader(202)
		case "/export/source":
			w.Write(raw)
		case "/import/source":
			data, _ := io.ReadAll(r.Body)
			z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			for _, f := range z.File {
				r, _ := f.Open()
				content, _ := io.ReadAll(r)
				r.Close()
				if strings.Contains(string(content), "SECRET") {
					t.Errorf("creator secret imported: %s", f.Name)
				}
			}
			imported = true
			io.WriteString(w, `{"imported":true}`)
		default:
			t.Errorf("unexpected guest %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer guest.Close()
	s.CubeProxyURL = guest.URL
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sandboxes" {
			var req cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&req)
			newToken = req.EnvVars["RUNTIMED_HTTP_TOKEN"]
			if newToken == sourceToken || len(newToken) != 64 {
				t.Error("fork reused source token")
			}
			if len(req.EnvVars) != 2 {
				t.Error("creator app env copied")
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"sandboxID":"vm-fork","templateID":"tpl-safe","trafficAccessToken":"new-traffic"}`)
			return
		}
		if r.URL.Path == "/sandboxes/vm-source/connect" {
			io.WriteString(w, `{"sandboxID":"vm-source","templateID":"tpl-safe"}`)
			return
		}
		t.Errorf("unexpected Cube request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}))
	defer management.Close()
	s.Cube, _ = cube.New(cube.Config{APIURL: management.URL, APIKey: "management"})
	creds, _ := json.Marshal(cubeCredentials{SupervisorToken: sourceToken, TrafficAccessToken: "source-traffic"})
	enc, nonce, _ := s.Secrets.Seal(creds)
	sourceID := "01M2QHT40D9S9W5MFNN32F1DXK"
	sb := &store.Sandbox{ID: sourceID, Status: "running", RuntimeProvider: "cube", Image: "cube-template:tpl-safe", AppID: sql.NullString{String: appID, Valid: true}, ExternalUserID: sql.NullString{String: "creator-user", Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-source", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: enc, TokenNonce: nonce}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, owner string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		return r.WithContext(auth.WithActor(r.Context(), auth.Actor{Kind: "service", Name: owner}))
	}
	r := request("POST", "/v1/snapshots", `{"source_sandbox_id":"`+sourceID+`","name":"published"}`, cfgTenant)
	out := httptest.NewRecorder()
	s.v1CreateSnapshot(out, r)
	if out.Code != 201 {
		t.Fatalf("publish %d %s", out.Code, out.Body)
	}
	var result v1Snapshot
	json.Unmarshal(out.Body.Bytes(), &result)
	snap, err := s.Store.GetSnapshot(context.Background(), result.ID)
	if err != nil || snap.Format != cubeSourceFormat {
		t.Fatalf("snapshot %+v %v", snap, err)
	}
	data, err := os.ReadFile(snap.ImagePath)
	if err != nil {
		t.Fatal(err)
	}
	z, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if len(z.File) != 2 {
		t.Fatalf("unsafe artifact files %d", len(z.File))
	}
	if legacy {
		s.CubeAllApps = true
		id := newULID()
		root := filepath.Join(s.LibraryRoot, id)
		for name, content := range map[string]string{
			"workspace/app/package.json": "{}", "workspace/app/src/main.ts": "published code",
			"workspace/app/.env": "SECRET_APP", "workspace/app/data/customer.json": "SECRET_DATA",
			".claude/auth.json": "SECRET_PROVIDER", ".runtimed/key": "SECRET_RUNTIME",
		} {
			p := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
		snap = &store.Snapshot{ID: id, Name: "Old published card", OwnerToken: cfgTenant, Status: "ready", Format: "raw", ImagePath: root, SourceAppID: sql.NullString{String: appID, Valid: true}}
		if err := s.Store.CreateSnapshot(context.Background(), snap); err != nil {
			t.Fatal(err)
		}
	}
	r = request("POST", "/v1/apps/"+appID+"/fork", `{"snapshot_id":"`+snap.ID+`","external_user_id":"remix-user","external_project_id":"remix-project"}`, cfgTenant)
	r.SetPathValue("id", appID)
	out = httptest.NewRecorder()
	s.v1ForkApp(out, r)
	if out.Code != 201 || !imported || strings.Contains(out.Body.String(), "sandbox_error") {
		t.Fatalf("fork %d %s imported=%v", out.Code, out.Body, imported)
	}
	var fork struct {
		App v1App `json:"app"`
	}
	json.Unmarshal(out.Body.Bytes(), &fork)
	newApp, err := s.Store.GetApp(context.Background(), fork.App.ID)
	if err != nil || newApp.ExternalUserID.String != "remix-user" || newApp.RuntimePreset.String != "react-vite" {
		t.Fatalf("fork ownership %+v %v", newApp, err)
	}
	configs, err := s.Store.ListAppConfig(context.Background(), newApp.ID)
	if err != nil || len(configs) != 0 {
		t.Fatal("creator config inherited")
	}
	if legacy {
		original, err := s.Store.GetSnapshot(context.Background(), snap.ID)
		if err != nil || original.Format != "raw" || original.ImagePath != snap.ImagePath {
			t.Fatal("legacy snapshot/link was mutated")
		}
	}
	r = request("POST", "/v1/apps/"+appID+"/fork", `{"snapshot_id":"`+snap.ID+`"}`, "other-tenant")
	r.SetPathValue("id", appID)
	out = httptest.NewRecorder()
	s.v1ForkApp(out, r)
	if out.Code != 404 {
		t.Fatalf("cross-tenant fork %d", out.Code)
	}
	r = request("POST", "/v1/snapshots", `{"source_sandbox_id":"`+sourceID+`","name":"stolen"}`, "other-tenant")
	out = httptest.NewRecorder()
	s.v1CreateSnapshot(out, r)
	if out.Code != 404 {
		t.Fatalf("cross-tenant publish %d", out.Code)
	}
}
