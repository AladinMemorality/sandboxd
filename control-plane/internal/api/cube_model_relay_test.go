package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const relayTestTask = "01M2QHT40D9S9W5MFNN32F1DY0"
const relayTestBridge = "project-bound-bridge-token-0123456789"

func relayFixture(t *testing.T, upstream http.HandlerFunc) (*Server, string, string, string, *atomic.Bool) {
	t.Helper()
	var active atomic.Bool
	active.Store(true)
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		status := runtime.Status{}
		if active.Load() {
			status.ActiveTask = &runtime.ActiveTask{ID: relayTestTask}
		}
		_ = json.NewEncoder(w).Encode(status)
	})
	target := httptest.NewServer(upstream)
	t.Cleanup(target.Close)
	s.AgentProxyURL = target.URL
	s.CubeAgentRelayOrigin = "https://relay.example"
	if err := s.Store.MarkRunning(context.Background(), id, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: relayTestTask, SandboxID: id, Agent: "claude-code", Prompt: "build", TimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	req := &v1TaskSubmitReq{Env: map[string]string{"BRIDGE_TOKEN": relayTestBridge}, TimeoutS: 60}
	if err := s.prepareCubeModelScope(context.Background(), id, relayTestTask, req); err != nil {
		t.Fatal(err)
	}
	// The public edge deliberately requires platform auth everywhere else.
	edge := httptest.NewServer(auth.NewMiddleware(&auth.Config{}, nil, nil, nil).Wrap(s.Handler()))
	t.Cleanup(edge.Close)
	path := "/v1/cube-model/" + id + "/" + relayTestTask + "/v1/messages"
	return s, edge.URL + path, req.Env["RUNTIMED_CUBE_AGENT_TOKEN"], id, &active
}

func relayRequest(t *testing.T, ctx context.Context, target, token, bridge string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(`{"model":"test","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Api-Key", token)
	r.Header.Set("X-Baarcha-Bridge", bridge)
	r.Header.Set("Authorization", "Bearer must-not-forward")
	r.Header.Set("Cookie", "session=must-not-forward")
	r.Header.Set("X-Baarcha-Project", "spoofed-project")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCubeModelRelayStreamingCancelAndScopedHeaders(t *testing.T) {
	cancelled := make(chan struct{})
	observed := make(chan http.Header, 1)
	_, target, token, _, _ := relayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/claude-code/anthropic/v1/messages" {
			t.Errorf("wrong upstream: %s", r.URL.Path)
		}
		observed <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "secret-cookie=host")
		w.Header().Set("Authorization", "host-secret")
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := relayRequest(t, ctx, target, token, relayTestBridge)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("relay: %d", resp.StatusCode)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("stream buffered/lost: %q %v", line, err)
	}
	headers := <-observed
	if headers.Get("X-Api-Key") != "sandboxd-proxy-injected" || headers.Get("X-Baarcha-Bridge") != relayTestBridge {
		t.Fatal("metering binding not preserved")
	}
	for _, key := range []string{"Authorization", "Cookie", "X-Baarcha-Project"} {
		if headers.Get(key) != "" {
			t.Fatalf("untrusted header forwarded: %s", key)
		}
	}
	if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Authorization") != "" {
		t.Fatal("upstream credential header leaked")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reach upstream")
	}
}

func TestCubeModelRelayRejectsWrongScopeStaleTaskAndForbiddenPaths(t *testing.T) {
	var calls atomic.Int32
	s, target, token, id, active := relayFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{"ok":true}`)) })
	for _, tc := range []struct {
		target, key, bridge string
		want                int
	}{
		{target, "wrong-token", relayTestBridge, 403},
		{target, token, "other-project-bridge-token-999", 403},
		{strings.Replace(target, id, "01M2QHT40D9S9W5MFNN32F1DY1", 1), token, relayTestBridge, 403},
		{target + "/arbitrary", token, relayTestBridge, 401},
		{target + "?target=http://169.254.169.254", token, relayTestBridge, 403},
	} {
		resp := relayRequest(t, context.Background(), tc.target, tc.key, tc.bridge)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %d %s", tc.target, resp.StatusCode, body)
		}
	}
	active.Store(false)
	resp := relayRequest(t, context.Background(), target, token, relayTestBridge)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("stale durable task allowed")
	}
	active.Store(true)
	if err := s.Store.FinishTask(context.Background(), relayTestTask, "succeeded", `{"status":"succeeded"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.CubeModelScopeFor(context.Background(), relayTestTask, id); err != store.ErrNotFound {
		t.Fatal("finished scope retained")
	}
	resp = relayRequest(t, context.Background(), target, token, relayTestBridge)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("finished task allowed")
	}
	if calls.Load() != 0 {
		t.Fatalf("forbidden requests reached upstream %d times", calls.Load())
	}
}

func TestCubeModelRelayRejectsUpstreamRedirectWithoutCredentialLeak(t *testing.T) {
	var leaks atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaks.Add(1) }))
	defer external.Close()
	_, target, token, _, _ := relayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", external.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	resp := relayRequest(t, context.Background(), target, token, relayTestBridge)
	defer resp.Body.Close()
	if resp.StatusCode != 502 || resp.Header.Get("Location") != "" || leaks.Load() != 0 {
		t.Fatalf("redirect not blocked: %d %s leaks=%d", resp.StatusCode, resp.Header.Get("Location"), leaks.Load())
	}
}

func TestCubeModelRelayRevokesOpenStreamWhenTaskFinishes(t *testing.T) {
	cancelled := make(chan struct{})
	s, target, token, _, _ := relayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := relayRequest(t, ctx, target, token, relayTestBridge)
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.FinishTask(context.Background(), relayTestTask, "cancelled", `{"status":"cancelled"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("finished task retained an open model stream")
	}
}

func TestCubeModelScopeStoresOnlyHashAndInjectsTaskCapability(t *testing.T) {
	s, _, _, id, _ := relayFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("scope preparation must not call upstream") })
	secondTask := "01M2QHT40D9S9W5MFNN32F1DY2"
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: secondTask, SandboxID: id, Agent: "claude-code", Prompt: "second"}); err != nil {
		t.Fatal(err)
	}
	req := &v1TaskSubmitReq{Env: map[string]string{"BRIDGE_TOKEN": relayTestBridge, "RUNTIMED_CUBE_AGENT_TOKEN": "caller-forgery", "RUNTIMED_CUBE_AGENT_BASE_URL": "https://untrusted.example"}}
	if err := s.prepareCubeModelScope(context.Background(), id, secondTask, req); err != nil {
		t.Fatal(err)
	}
	token, err := s.cubeModelToken(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if req.Env["RUNTIMED_CUBE_AGENT_TOKEN"] != token || token == strings.Repeat("a", 64) {
		t.Fatal("guest did not receive derived scoped capability")
	}
	if req.Env["RUNTIMED_CUBE_AGENT_BASE_URL"] != "https://relay.example/v1/cube-model/"+id+"/"+secondTask {
		t.Fatal("caller controlled relay destination")
	}
	scope, err := s.Store.CubeModelScopeFor(context.Background(), secondTask, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.BridgeHash) != 32 || strings.Contains(string(scope.BridgeHash), relayTestBridge) {
		t.Fatal("plaintext bridge token stored")
	}
	raw, _ := json.Marshal(scope)
	if strings.Contains(string(raw), "BridgeHash") {
		t.Fatal("bridge capability leaked in scope JSON")
	}
	thirdTask := "01M2QHT40D9S9W5MFNN32F1DY3"
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: thirdTask, SandboxID: id, Agent: "claude-code", Prompt: "expired"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.CreateCubeModelScope(context.Background(), store.CubeModelScope{TaskID: thirdTask, SandboxID: id, BridgeHash: scope.BridgeHash, ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.CubeModelScopeFor(context.Background(), thirdTask, id); err != store.ErrNotFound {
		t.Fatal("expired scope accepted")
	}
}

func TestCubeRelayConfiguredTaskRejectsUnsupportedAgentAndCallerOverrides(t *testing.T) {
	s, _, _, id, _ := relayFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("invalid submit must not call upstream") })
	for _, body := range []string{
		`{"prompt":"build","agent":"opencode","env":{"BRIDGE_TOKEN":"project-bound-bridge-token-0123456789"}}`,
		`{"prompt":"build","agent":"claude-code"}`,
		`{"prompt":"build","agent":"claude-code","env":{"BRIDGE_TOKEN":"project-bound-bridge-token-0123456789","RUNTIMED_CUBE_AGENT_TOKEN":"caller"}}`,
	} {
		w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/tasks", body, cfgTenant)
		if w.Code != 400 {
			t.Fatalf("invalid relay submit: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestCubeModelRelayCanonicalPathQueryAndSingleHeaders(t *testing.T) {
	var calls atomic.Int32
	_, target, token, _, _ := relayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.RawQuery != "beta=true" {
			t.Errorf("unexpected query forwarded: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	for _, test := range []struct {
		target                  string
		doubleKey, doubleBridge bool
		want                    int
	}{
		{target + "?beta=true", false, false, 200},
		{target + "?beta=true&target=evil", false, false, 403},
		{target + "?beta=%74rue", false, false, 403},
		{strings.Replace(target, "/v1/messages", "/%76%31/messages", 1), false, false, 401},
		{target, true, false, 403},
		{target, false, true, 403},
	} {
		req, err := http.NewRequest("POST", test.target, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Api-Key", token)
		req.Header.Set("X-Baarcha-Bridge", relayTestBridge)
		if test.doubleKey {
			req.Header.Add("X-Api-Key", token)
		}
		if test.doubleBridge {
			req.Header.Add("X-Baarcha-Bridge", relayTestBridge)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != test.want {
			t.Errorf("%s dupkey=%t dupbridge=%t: %d %s", test.target, test.doubleKey, test.doubleBridge, resp.StatusCode, body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("noncanonical or duplicate credentials reached upstream: %d", calls.Load())
	}
}

func TestCubeClaudeWithoutScopedRelayFailsClearlyBeforeTaskStart(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("disabled relay must fail before contacting guest")
	})
	w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/tasks", `{"agent":"claude-code","prompt":"build"}`, cfgTenant)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "model_relay_disabled") {
		t.Fatalf("disabled relay is ambiguous: %d %s", w.Code, w.Body.String())
	}
	tasks, err := s.Store.ListTasksForSandbox(context.Background(), id, 10)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("disabled relay started task: %v %v", tasks, err)
	}
}
