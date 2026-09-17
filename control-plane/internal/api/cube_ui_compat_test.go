package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubePreviewAccessIssuesScopedTokenWithoutWaking(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("preview upstream called") })
	path := "/v1/sandboxes/" + cubePreviewTestID + "/preview-access"
	denied := cubeRequest(s, "POST", path, "", "other-tenant")
	if denied.Code != 404 {
		t.Fatalf("wrongtenant %d", denied.Code)
	}
	w := cubeRequest(s, "POST", path, "", cfgTenant)
	if w.Code != 200 {
		t.Fatalf("issue %d %s", w.Code, w.Body.String())
	}
	var got struct {
		URL       string `json:"url"`
		AccessURL string `json:"access_url"`
		Token     string `json:"token"`
		Expires   string `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	claims, reason := auth.CheckPreviewAccess(got.Token, cubePreviewTestID, "owner-one", s.authCfg().PreviewSecrets, time.Now())
	if reason != "" || claims.Exp > time.Now().Add(5*time.Minute).Unix() {
		t.Fatalf("claims %+v %s", claims, reason)
	}
	u, err := url.Parse(got.AccessURL)
	if err != nil || u.Path != "/__sandboxd/preview-auth" || u.Query().Get("token") != got.Token || u.Query().Get("path") != "/" {
		t.Fatalf("bad handoff %v", err)
	}
	if got.URL != s.previewURL(cubePreviewTestID, 3000) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unstable URL or cached token")
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("token issue woke VM")
	}
	row, _ := s.Store.Get(context.Background(), cubePreviewTestID)
	raw, _ := json.Marshal(toRespRow(row))
	if !strings.Contains(string(raw), `"runtime_provider":"cube"`) || strings.Contains(string(raw), got.Token) {
		t.Fatal("raw response provider or secret exposure")
	}
	w = cubeRequest(s, "GET", "/v1/sandboxes/"+cubePreviewTestID, "", cfgTenant)
	if !strings.Contains(w.Body.String(), `"runtime_provider":"cube"`) || strings.Contains(w.Body.String(), got.Token) {
		t.Fatal("normal GET provider or token exposure")
	}
	s.Auth = auth.NewMiddleware(&auth.Config{}, nil, nil, s.Log)
	w = cubeRequest(s, "POST", path, "", cfgTenant)
	if w.Code != 503 {
		t.Fatalf("missing signingkey %d", w.Code)
	}
}

func TestCubeRecreateAppliesConfigAndPreservesStableRuntime(t *testing.T) {
	var mu sync.Mutex
	revision := ""
	applies := 0
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/config" {
			var req runtime.AppConfigRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Env["COLOR"] != "blue" {
				t.Error("config lost")
			}
			revision = req.Revision
			applies++
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(runtime.Status{AppConfigRevision: revision})
	})
	sb, _ := s.Store.Get(context.Background(), id)
	w := cubeRequest(s, "POST", "/v1/apps/"+sb.AppID.String+"/config", `{"key":"COLOR","value":"blue","access_policy":"runtime_access"}`, cfgTenant)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	path := "/v1/sandboxes/" + id + "/recreate"
	w = cubeRequest(s, "POST", path, "", "other-owner")
	if w.Code != 404 {
		t.Fatalf("wrong owner %d", w.Code)
	}
	for i := 0; i < 2; i++ {
		w = cubeRequest(s, "POST", path, "", cfgTenant)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"runtime_provider":"cube"`) {
			t.Fatalf("recreate %d %s", w.Code, w.Body.String())
		}
	}
	b, err := s.Store.GetRuntimeBinding(context.Background(), id)
	if err != nil || b.RuntimeID != "vm-tasks" || b.ConfigRevision != b.ConfigAppliedRevision {
		t.Fatalf("binding changed %+v %v", b, err)
	}
	mu.Lock()
	count := applies
	mu.Unlock()
	if count != 1 {
		t.Fatalf("unneeded restarts %d", count)
	}
	taskID := "01M2QHT40D9S9W5MFNN32F1DXW"
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: taskID, SandboxID: id, Agent: "opencode", Prompt: "busy"}); err != nil {
		t.Fatal(err)
	}
	w = cubeRequest(s, "POST", path, "", cfgTenant)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "task_in_progress") {
		t.Fatalf("active task recreate %d %s", w.Code, w.Body.String())
	}
}

func TestCubeUncertainSubmissionReturnsDurableAcceptedTask(t *testing.T) {
	var mu sync.Mutex
	taskID := ""
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/status":
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/tasks" && r.Method == "POST":
			var req runtime.StartTaskRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			taskID = req.TaskID
			mu.Unlock()
			http.Error(w, "response lost", 502)
		case strings.HasSuffix(r.URL.Path, "/result"):
			mu.Lock()
			task := taskID
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(runtime.TaskResult{ID: task, Status: runtime.TaskSucceeded})
		default:
			w.WriteHeader(404)
		}
	})
	w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/tasks", `{"prompt":"do it once","agent":"opencode"}`, cfgTenant)
	if w.Code != 202 {
		t.Fatalf("uncertain submit retries allowed %d %s", w.Code, w.Body.String())
	}
	var response struct {
		ID      string `json:"id"`
		Pending bool   `json:"submission_pending"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if response.ID == "" || !response.Pending {
		t.Fatal("missing durable handle")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		row, err := s.Store.GetTask(context.Background(), response.ID)
		if err == nil && row.Status == "succeeded" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("accepted task was not reconciled")
}
