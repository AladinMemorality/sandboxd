package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeFilesRouteWithoutHostWorkspaceAndEnforceOwner(t *testing.T) {
	s, appID := newConfigTestServer(t)
	token := strings.Repeat("ab", 32)
	var calls atomic.Int32
	var operation string
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("cube-traffic-access-token") != "traffic" {
			t.Error("missing guest auth")
		}
		switch r.URL.Path {
		case "/status":
			io.WriteString(w, `{}`)
		case "/files":
			if r.Method == http.MethodPut {
				b, _ := io.ReadAll(r.Body)
				if string(b) != "new bytes" {
					t.Error("write body lost")
				}
				io.WriteString(w, `{"path":"file.txt","size":9}`)
			} else {
				if r.URL.Query().Get("path") != "." || r.URL.Query().Get("recursive") != "true" {
					t.Error("platform file-list query lost")
				}
				io.WriteString(w, `{"path":".","recursive":true,"entries":[{"path":"src/main.ts","type":"file","size":12}]}`)
			}
			operation = "files"
		case "/files/content":
			io.WriteString(w, "guest contents")
			operation = "read"
		case "/export":
			io.WriteString(w, "zip-fixture")
			operation = "export"
		case "/processes/web/logs":
			io.WriteString(w, `{"process":"web","lines":["ready"]}`)
			operation = "logs"
		default:
			t.Errorf("unexpected guest path %s", r.URL.Path)
		}
	}))
	defer guest.Close()
	s.CubeProxyURL = guest.URL
	credentials, _ := json.Marshal(cubeCredentials{SupervisorToken: token, TrafficAccessToken: "traffic"})
	sealed, nonce, _ := s.Secrets.Seal(credentials)
	id := "01M2QHT40D9S9W5MFNN32F1DXK"
	sb := &store.Sandbox{ID: id, Status: "running", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-files", TemplateID: "tpl", Domain: "cube.test", TokenCiphertext: sealed, TokenNonce: nonce}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, want string
		handler            http.HandlerFunc
	}{
		{"GET", "/files?path=.&recursive=true", "files", s.v1ListFiles}, {"GET", "/files/content?path=file.txt", "read", s.v1FileContent}, {"PUT", "/files?path=file.txt", "files", s.v1PutFile}, {"GET", "/export", "export", s.v1Export}, {"GET", "/processes/web/logs", "logs", s.v1ProcessLogs},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("new bytes"))
			req.SetPathValue("id", id)
			req.SetPathValue("name", "web")
			req = req.WithContext(auth.WithActor(req.Context(), auth.Actor{Kind: "service", Name: cfgTenant}))
			out := httptest.NewRecorder()
			tc.handler(out, req)
			if out.Code != 200 || operation != tc.want {
				t.Fatalf("route %d %s operation=%s", out.Code, out.Body, operation)
			}
			if tc.method == "GET" && tc.want == "files" && !strings.Contains(out.Body.String(), `"path":"src/main.ts"`) {
				t.Fatalf("platform entries response lost: %s", out.Body)
			}
			before := calls.Load()
			req = req.WithContext(auth.WithActor(req.Context(), auth.Actor{Kind: "service", Name: "other-owner"}))
			out = httptest.NewRecorder()
			tc.handler(out, req)
			if out.Code != 404 || calls.Load() != before {
				t.Fatalf("cross-owner route %d calls=%d", out.Code, calls.Load()-before)
			}
		})
	}
	// A stopped guest is resumed through Cube, without requiring Docker or mounts.
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/sandboxes/vm-files/connect" {
			t.Errorf("unexpected management request %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"sandboxID":"vm-files","templateID":"tpl"}`)
	}))
	defer management.Close()
	s.Cube, _ = cube.New(cube.Config{APIURL: management.URL, APIKey: "management"})
	s.Store.MarkStopped(context.Background(), id)
	req := httptest.NewRequest("GET", "/files/content?path=file.txt", nil)
	req.SetPathValue("id", id)
	req = req.WithContext(auth.WithActor(req.Context(), auth.Actor{Kind: "service", Name: cfgTenant}))
	out := httptest.NewRecorder()
	s.v1FileContent(out, req)
	if out.Code != 200 {
		t.Fatalf("paused workspace: %d %s", out.Code, out.Body)
	}
	updated, _ := s.Store.Get(context.Background(), id)
	if updated.Status != "running" {
		t.Fatalf("resume state: %s", updated.Status)
	}
}
