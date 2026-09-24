package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func cubeTaskFixture(t *testing.T, guest http.HandlerFunc) (*Server, string, *atomic.Int32) {
	t.Helper()
	s, appID := newConfigTestServer(t)
	s.Locks = idlock.New()
	app, err := s.Store.GetApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "tasks.db")+"?_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = durable.Close() })
	s.Store = durable
	if err := durable.CreateApp(context.Background(), app); err != nil {
		t.Fatal(err)
	}

	var connections atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/connect") {
			connections.Add(1)
		}
		if r.Method == "DELETE" || strings.HasSuffix(r.URL.Path, "/pause") {
			w.WriteHeader(204)
			return
		}
		_, _ = w.Write([]byte(`{"sandboxID":"vm-tasks","templateID":"tpl-tasks","state":"running"}`))
	}))
	t.Cleanup(upstream.Close)
	s.Cube, err = cube.New(cube.Config{APIURL: upstream.URL, APIKey: "api-test"})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(guest)
	t.Cleanup(proxy.Close)
	s.CubeProxyURL = proxy.URL
	credentials, _ := json.Marshal(cubeCredentials{SupervisorToken: strings.Repeat("a", 64), TrafficAccessToken: "private-token"})
	sealed, nonce, err := s.Secrets.Seal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	id := "01M2QHT40D9S9W5MFNN32F1DXK"
	sb := &store.Sandbox{ID: id, Status: "stopped", RuntimeProvider: "cube", AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-tasks", TemplateID: "tpl-tasks", Domain: "cube.test", TokenCiphertext: sealed, TokenNonce: nonce}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	return s, id, &connections
}

func cubeRequest(s *Server, method, path, body, owner string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: owner, Kind: "service"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestCubeTaskWorkflowUsesRemoteGuestAndPersistsResults(t *testing.T) {
	var mu sync.Mutex
	taskID := ""
	done := false
	messages := 0
	terminal := make(chan struct{})
	var once sync.Once
	s, id, connections := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) || r.Header.Get("cube-traffic-access-token") != "private-token" {
			t.Error("guest credentials absent")
		}
		mu.Lock()
		current, finished := taskID, done
		mu.Unlock()
		switch {
		case r.URL.Path == "/status":
			if current != "" && !finished {
				fmt.Fprintf(w, `{"active_task":{"id":%q}}`, current)
			} else {
				_, _ = w.Write([]byte(`{}`))
			}
		case r.Method == "POST" && r.URL.Path == "/tasks":
			var request runtime.StartTaskRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			mu.Lock()
			taskID = request.TaskID
			mu.Unlock()
			w.WriteHeader(202)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			mu.Lock()
			messages++
			mu.Unlock()
			w.WriteHeader(202)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			mu.Lock()
			done = true
			mu.Unlock()
			once.Do(func() { close(terminal) })
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/revert"):
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/result"):
			if !finished {
				w.WriteHeader(409)
				return
			}
			_ = json.NewEncoder(w).Encode(runtime.TaskResult{ID: current, Status: runtime.TaskCancelled, FilesChanged: []string{}})
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			select {
			case <-terminal:
			case <-r.Context().Done():
				return
			}
			mu.Lock()
			current = taskID
			mu.Unlock()
			data, _ := json.Marshal(runtime.TaskResult{ID: current, Status: runtime.TaskCancelled})
			_ = json.NewEncoder(w).Encode(runtime.Event{ID: 1, Type: runtime.EventDone, Data: data})
		default:
			http.Error(w, "unexpected guest route", 404)
		}
	})
	defer once.Do(func() { close(terminal) })
	base := "/v1/sandboxes/" + id
	w := cubeRequest(s, "POST", base+"/tasks", `{"prompt":"build app","agent":"opencode"}`, cfgTenant)
	if w.Code != 202 {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	if connections.Load() < 1 {
		t.Fatal("stopped task did not wake Cube")
	}
	mu.Lock()
	submitted := taskID
	mu.Unlock()
	task, err := s.Store.GetTask(context.Background(), submitted)
	if err != nil || task.SandboxID != id {
		t.Fatalf("accepted task not durable: %v", err)
	}
	if w = cubeRequest(s, "POST", base+"/stop", "", cfgTenant); w.Code != 409 {
		t.Fatalf("paused an active task: %d %s", w.Code, w.Body.String())
	}
	path := base + "/tasks/" + submitted
	for _, endpoint := range []string{"/messages", "/cancel", "/revert", "/events"} {
		method := "POST"
		if endpoint == "/events" {
			method = "GET"
		}
		if w = cubeRequest(s, method, path+endpoint, `{"message_id":"b981325c-dc86-4a60-bd41-08f90a70ea38","prompt":"more"}`, "other-owner"); w.Code != 404 {
			t.Fatalf("cross-owner %s: %d", endpoint, w.Code)
		}
	}
	w = cubeRequest(s, "POST", path+"/messages", `{"message_id":"b981325c-dc86-4a60-bd41-08f90a70ea38","prompt":"more"}`, cfgTenant)
	if w.Code != 202 {
		t.Fatalf("input: %d %s", w.Code, w.Body.String())
	}
	w = cubeRequest(s, "POST", path+"/cancel", "", cfgTenant)
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	w = cubeRequest(s, "GET", path+"/events", "", cfgTenant)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatalf("events: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, err = s.Store.GetTask(context.Background(), submitted)
		if err == nil && task.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher result not persisted: %+v %v", task, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	w = cubeRequest(s, "POST", path+"/revert", "", cfgTenant)
	if w.Code != 200 {
		t.Fatalf("revert: %d %s", w.Code, w.Body.String())
	}
	w = cubeRequest(s, "DELETE", base, "", cfgTenant)
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	w = cubeRequest(s, "GET", path, "", cfgTenant)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatalf("durable result: %d %s", w.Code, w.Body.String())
	}
	if w = cubeRequest(s, "GET", path, "", "other-owner"); w.Code != 404 {
		t.Fatal("deleted sandbox leaked task result")
	}
	if w = cubeRequest(s, "GET", base+"/tasks", "", "other-owner"); w.Code != 404 {
		t.Fatal("deleted sandbox leaked task list")
	}
	if err := s.Store.Create(context.Background(), &store.Sandbox{ID: id, Status: "running"}); err != store.ErrConflict {
		t.Fatalf("recycled Cube ID can bypass archived tenant boundary: %v", err)
	}
	mu.Lock()
	count := messages
	mu.Unlock()
	if count != 1 {
		t.Fatalf("guest got %d input messages", count)
	}
}

func TestCubeReconcileRecoversCreatingAndCompletedTaskWithoutHostFiles(t *testing.T) {
	taskID := "01M2QHT40D9S9W5MFNN32F1DXY"
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/result") {
			_ = json.NewEncoder(w).Encode(runtime.TaskResult{ID: taskID, Status: runtime.TaskSucceeded})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if err := s.Store.MarkError(context.Background(), id, "interrupted creation"); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: taskID, SandboxID: id, Agent: "opencode", Prompt: "recover"}); err != nil {
		t.Fatal(err)
	}
	s.ReconcileCube(context.Background())
	s.ReconcileTasks(context.Background())
	sb, err := s.Store.Get(context.Background(), id)
	if err != nil || sb.Status != "running" {
		t.Fatalf("runtime not recovered: %+v %v", sb, err)
	}
	task, err := s.Store.GetTask(context.Background(), taskID)
	if err != nil || task.Status != "succeeded" {
		t.Fatalf("task not recovered: %+v %v", task, err)
	}
}

func TestCubeWatcherUsesGuestResultWhenEventStreamDisconnects(t *testing.T) {
	taskID := "01M2QHT40D9S9W5MFNN32F1DXZ"
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/result") {
			_ = json.NewEncoder(w).Encode(runtime.TaskResult{ID: taskID, Status: runtime.TaskSucceeded})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: taskID, SandboxID: id, Agent: "opencode", Prompt: "complete"}); err != nil {
		t.Fatal(err)
	}
	s.watchTaskWindow(id, taskID, time.Second)
	task, err := s.Store.GetTask(context.Background(), taskID)
	if err != nil || task.Status != "succeeded" {
		t.Fatalf("stream disconnect lost completed task: %+v %v", task, err)
	}
}

func TestCubeTaskRecoveryPreservesPendingDuringGuestOutage(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "temporary", 503) })
	taskID := "01M2QHT40D9S9W5MFNN32F1DXV"
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: taskID, SandboxID: id, Agent: "opencode", Prompt: "recover"}); err != nil {
		t.Fatal(err)
	}
	s.ReconcileTasks(context.Background())
	task, err := s.Store.GetTask(context.Background(), taskID)
	if err != nil || task.Status != "running" {
		t.Fatalf("outage fabricated failure: %+v %v", task, err)
	}
	if _, err := s.Store.GetRuntimeBinding(context.Background(), id); err != nil {
		t.Fatalf("outage removed binding: %v", err)
	}
}
