package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const cubeModelBodyLimit = 16 << 20

func (s *Server) cubeModelToken(ctx context.Context, id string) (string, error) {
	if s.Secrets == nil {
		return "", errors.New("relay unavailable")
	}
	b, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		return "", err
	}
	plaintext, err := s.Secrets.Open(b.TokenCiphertext, b.TokenNonce)
	if err != nil {
		return "", err
	}
	var creds cubeCredentials
	if json.Unmarshal(plaintext, &creds) != nil || creds.SupervisorToken == "" {
		return "", errors.New("relay unavailable")
	}
	mac := hmac.New(sha256.New, []byte(creds.SupervisorToken))
	_, _ = mac.Write([]byte("cube-model-relay"))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func validCubeBridgeToken(value string) bool {
	return len(value) >= 16 && len(value) <= 4096 && !strings.ContainsAny(value, "\r\n\x00")
}

// prepareCubeModelScope receives only the already authenticated platform task
// request. The guest gets a scoped capability, never the upstream provider key.
func (s *Server) prepareCubeModelScope(ctx context.Context, id, taskID string, req *v1TaskSubmitReq) error {
	if s.CubeAgentRelayOrigin == "" {
		return nil
	}
	if !validCubeBridgeToken(req.Env["BRIDGE_TOKEN"]) {
		return errors.New("Cube model relay requires this project's bridge token")
	}
	token, err := s.cubeModelToken(ctx, id)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(req.Env["BRIDGE_TOKEN"]))
	if err := s.Store.CreateCubeModelScope(ctx, store.CubeModelScope{TaskID: taskID, SandboxID: id, BridgeHash: digest[:], ExpiresAt: time.Now().Add(watchWindowFor(req.TimeoutS))}); err != nil {
		return err
	}
	env := make(map[string]string, len(req.Env)+2)
	for key, value := range req.Env {
		if !strings.HasPrefix(strings.ToUpper(key), "RUNTIMED_CUBE_AGENT_") {
			env[key] = value
		}
	}
	env["RUNTIMED_CUBE_AGENT_BASE_URL"] = strings.TrimRight(s.CubeAgentRelayOrigin, "/") + "/v1/cube-model/" + id + "/" + taskID
	if s.cubeEgress != nil {
		env["RUNTIMED_CUBE_AGENT_BASE_URL"] = "http://127.0.0.1:3032/__cube/model/v1/cube-model/" + id + "/" + taskID
		env["BRIDGE_URL"] = "http://127.0.0.1:3032/__cube/bridge"
	}
	env["RUNTIMED_CUBE_AGENT_TOKEN"] = token
	req.Env = env
	return nil
}

// cubeModelRelay is self-authenticated; only these exact Anthropic operations
// bypass ordinary platform API authentication. URLs/providers are never input.
func (s *Server) cubeModelRelay(w http.ResponseWriter, r *http.Request) {
	deny := func() {
		writeV1Err(w, http.StatusForbidden, "forbidden", "model relay capability is invalid or inactive")
	}
	if s.CubeAgentRelayOrigin == "" || s.AgentProxyURL == "" || s.Store == nil || s.Cube == nil {
		http.NotFound(w, r)
		return
	}
	id, taskID := r.PathValue("sandboxID"), r.PathValue("taskID")
	if r.Method != http.MethodPost || !isULID(id) || !isULID(taskID) || r.URL.RawPath != "" || (r.URL.RawQuery != "" && r.URL.RawQuery != "beta=true") {
		deny()
		return
	}
	suffix := "/v1/messages"
	if strings.HasSuffix(r.URL.Path, "/count_tokens") {
		suffix += "/count_tokens"
	}
	expectedPath := "/v1/cube-model/" + id + "/" + taskID + suffix
	if r.URL.Path != expectedPath {
		deny()
		return
	}
	if len(r.Header.Values("X-Api-Key")) != 1 || len(r.Header.Values("X-Baarcha-Bridge")) != 1 {
		deny()
		return
	}
	token, err := s.cubeModelToken(r.Context(), id)
	if err != nil || subtle.ConstantTimeCompare([]byte(token), []byte(r.Header.Get("X-Api-Key"))) != 1 {
		deny()
		return
	}
	scope, err := s.Store.CubeModelScopeFor(r.Context(), taskID, id)
	if err != nil {
		deny()
		return
	}
	bridge := r.Header.Get("X-Baarcha-Bridge")
	digest := sha256.Sum256([]byte(bridge))
	if !validCubeBridgeToken(bridge) || subtle.ConstantTimeCompare(scope.BridgeHash, digest[:]) != 1 {
		deny()
		return
	}
	// Check live guest ownership, not only a stale durable running flag.
	probe, cancelProbe := context.WithTimeout(r.Context(), 3*time.Second)
	status, err := s.runtimeClientFor(id).Status(probe)
	cancelProbe()
	if err != nil || status.ActiveTask == nil || status.ActiveTask.ID != taskID {
		deny()
		return
	}
	target, err := url.Parse(s.AgentProxyURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		writeV1Err(w, 503, "runtime_unavailable", "model relay upstream unavailable")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// Revoke already-open streams promptly when the task finishes/expires.
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.Store.CubeModelScopeFor(ctx, taskID, id); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	request := r.Clone(ctx)
	request.Body = http.MaxBytesReader(w, r.Body, cubeModelBodyLimit)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 16 << 10
	transport.ResponseHeaderTimeout = 2 * time.Minute
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.URL.Path = strings.TrimRight(target.Path, "/") + "/claude-code/anthropic" + suffix
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			pr.Out.Header = make(http.Header)
			for _, key := range []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta"} {
				if value := r.Header.Get(key); value != "" {
					pr.Out.Header.Set(key, value)
				}
			}
			pr.Out.Header.Set("X-Api-Key", "sandboxd-proxy-injected")
			pr.Out.Header.Set("X-Baarcha-Bridge", bridge)
		}, Transport: transport, FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				_ = resp.Body.Close()
				return errors.New("model relay redirects are forbidden")
			}
			headers := make(http.Header)
			for _, key := range []string{"Content-Type", "Retry-After", "Request-Id", "X-Request-Id"} {
				if value := resp.Header.Get(key); value != "" {
					headers.Set(key, value)
				}
			}
			headers.Set("Cache-Control", "private, no-store")
			resp.Header = headers
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeV1Err(w, 502, "model_upstream_unavailable", "model relay upstream unavailable")
		},
	}
	proxy.ServeHTTP(w, request)
}
