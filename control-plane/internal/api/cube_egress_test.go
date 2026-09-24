package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func reverseFixture(t *testing.T, upstream http.HandlerFunc) (*Server, string, *egress.Guest, string) {
	t.Helper()
	guest, err := egress.NewGuest(egress.GuestOptions{Authenticate: func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer "+strings.Repeat("a", 64) && r.Header.Get("cube-traffic-access-token") == "private-token"
	}})
	if err != nil {
		t.Fatal(err)
	}
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/egress/channel" {
			guest.ChannelHandler().ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/status" {
			json.NewEncoder(w).Encode(runtime.Status{ActiveTask: &runtime.ActiveTask{ID: relayTestTask}})
			return
		}
		io.WriteString(w, "{}")
	})
	target := httptest.NewServer(upstream)
	t.Cleanup(target.Close)
	s.AgentProxyURL = target.URL
	s.CubeAgentRelayOrigin = "https://relay.example"
	ctx, cancel := context.WithCancel(context.Background())
	cfg := CubeEgressConfig{Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("65.108.225.153/32")}, ProtectedDomains: []string{"baarcha.tn"}}, BridgeURL: "https://bridge.example/api/bridge"}
	if err = s.ConfigureCubeEgress(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Only the fixture bypasses the configured HTTPS service origin; the guest
	// still cannot choose an upstream URL, and startup rejects plaintext config.
	s.cubeEgress.config.BridgeURL = target.URL + "/api/bridge"
	if err = s.Store.MarkRunning(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.CreateTask(ctx, &store.Task{TaskID: relayTestTask, SandboxID: id, Agent: "claude-code", Prompt: "fixture", TimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	request := &v1TaskSubmitReq{Env: map[string]string{"BRIDGE_TOKEN": relayTestBridge}, TimeoutS: 60}
	if err = s.prepareCubeModelScope(ctx, id, relayTestTask, request); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(request.Env["RUNTIMED_CUBE_AGENT_BASE_URL"], "http://127.0.0.1:3032/__cube/model/") || request.Env["BRIDGE_URL"] != "http://127.0.0.1:3032/__cube/bridge" {
		t.Fatal("fixed service routing not injected")
	}
	if err = s.ensureCubeEgress(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.cubeEgress.mu.Lock()
		entry := s.cubeEgress.sessions[id]
		s.cubeEgress.mu.Unlock()
		cancel()
		guest.Close()
		if entry != nil {
			select {
			case <-entry.done:
			case <-time.After(3 * time.Second):
				t.Error("reverse session leaked")
			}
		}
	})
	return s, id, guest, request.Env["RUNTIMED_CUBE_AGENT_TOKEN"]
}

func TestCubeReverseServicesPreserveModelMeteringAndBridgeScope(t *testing.T) {
	var modelCalls, bridgeCalls atomic.Int32
	s, id, g, token := reverseFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/claude-code/anthropic/v1/messages":
			modelCalls.Add(1)
			if r.Header.Get("X-Baarcha-Bridge") != relayTestBridge || r.Header.Get("X-Api-Key") != "sandboxd-proxy-injected" {
				t.Error("model metering identity lost")
			}
		case "/api/bridge":
			bridgeCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+relayTestBridge {
				t.Error("bridge capability lost")
			}
		default:
			t.Errorf("unexpected service path %q", r.URL.Path)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("guest header leaked")
		}
		w.Header().Set("Authorization", "host-secret")
		w.Header().Set("Set-Cookie", "host-secret")
		io.WriteString(w, `{"ok":true}`)
	})
	_ = s
	model := httptest.NewServer(g.ServiceHandler("model"))
	defer model.Close()
	bridge := httptest.NewServer(g.ServiceHandler("bridge"))
	defer bridge.Close()
	resp := relayRequest(t, context.Background(), model.URL+"/v1/cube-model/"+id+"/"+relayTestTask+"/v1/messages", token, relayTestBridge)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("model status %d", resp.StatusCode)
	}
	for _, cap := range []string{relayTestBridge, "other-project-token-12345"} {
		req, _ := http.NewRequest("POST", bridge.URL+"/api/bridge", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+cap)
		req.Header.Set("Cookie", "guest-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		want := 200
		if cap != relayTestBridge {
			want = 403
		}
		if resp.StatusCode != want || resp.Header.Get("Authorization") != "" || resp.Header.Get("Set-Cookie") != "" {
			t.Fatalf("bridge status/headers: %d %v", resp.StatusCode, resp.Header)
		}
	}
	if modelCalls.Load() != 1 || bridgeCalls.Load() != 1 {
		t.Fatalf("unexpected callback counts %d/%d", modelCalls.Load(), bridgeCalls.Load())
	}
}
func TestCubeReverseBridgeRevokesOpenRequestWhenTaskFinishes(t *testing.T) {
	revoked := make(chan struct{})
	s, id, g, _ := reverseFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(revoked)
		case <-time.After(5 * time.Second):
		}

	})
	bridge := httptest.NewServer(g.ServiceHandler("bridge"))
	defer bridge.Close()
	req, _ := http.NewRequest("POST", bridge.URL+"/api/bridge", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+relayTestBridge)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err = bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.FinishTask(context.Background(), relayTestTask, "cancelled", `{"status":"cancelled"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-revoked:
	case <-time.After(2 * time.Second):
		t.Fatal("finished task retained bridge request")
	}
	s.stopCubeEgress(id)
}
func TestCubeReverseManagerReconnectStopAndCredentialRotation(t *testing.T) {
	s, id, g, _ := reverseFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "{}") })
	s.cubeEgress.mu.Lock()
	old := s.cubeEgress.sessions[id]
	s.cubeEgress.mu.Unlock()
	binding, err := s.Store.GetRuntimeBinding(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.Secrets.Open(binding.TokenCiphertext, binding.TokenNonce)
	if err != nil {
		t.Fatal(err)
	}
	cipher, nonce, err := s.Secrets.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB().Exec("UPDATE runtime_binding SET token_ciphertext=?,token_nonce=? WHERE sandbox_id=?", cipher, nonce, id); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureCubeEgress(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	s.cubeEgress.mu.Lock()
	fresh := s.cubeEgress.sessions[id]
	s.cubeEgress.mu.Unlock()
	if fresh == old || fresh.generation == old.generation {
		t.Fatal("credential rotation reused session identity")
	}
	select {
	case <-old.done:
	case <-time.After(2 * time.Second):
		t.Fatal("old generation not revoked")
	}
	s.stopCubeEgress(id)
	select {
	case <-fresh.done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not join session")
	}
	if err = s.Store.MarkStoppedAt(context.Background(), id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureCubeEgress(context.Background(), id); err == nil {
		t.Fatal("stopped runtime accepted channel")
	}
	if err = s.Store.MarkRunning(context.Background(), id, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureCubeEgress(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if !g.Ready() {
		t.Fatal("resumed runtime did not reconnect")
	}
}
func TestCubeReverseStartupRejectsIncompleteServicePolicy(t *testing.T) {
	s, _, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	s.AgentProxyURL = "http://127.0.0.1:9999"
	s.CubeAgentRelayOrigin = "https://relay.example"
	cfg := CubeEgressConfig{Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("65.108.225.153/32")}}, BridgeURL: "https://bridge.example/api/bridge"}
	for _, bad := range []string{"http://bridge.example/api/bridge", "https://bridge.example/other", "https://user:secret@bridge.example/api/bridge", "https://bridge.example/api/bridge?"} {
		cfg.BridgeURL = bad
		if err := s.ConfigureCubeEgress(context.Background(), cfg); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	cfg.BridgeURL = "https://bridge.example/api/bridge"
	cfg.Policy.ProtectedPrefixes = nil
	if err := s.ConfigureCubeEgress(context.Background(), cfg); err == nil {
		t.Fatal("missing inventory accepted")
	}
}
