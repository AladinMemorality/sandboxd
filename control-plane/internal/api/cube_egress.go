package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Reverse egress is an explicit deployment option. It adds no guest NIC
// allowance: public destinations and fixed services use host-initiated channels.
type CubeEgressConfig struct {
	Policy          egress.Policy
	BridgeURL       string
	AppHTTPServices map[string][]egress.HTTPService
}

type cubeEgressSession struct {
	generation string
	cancel     context.CancelFunc
	mu         sync.Mutex
	ready      chan struct{}
	connected  bool
	done       chan struct{}
}

type cubeEgressManager struct {
	ctx      context.Context
	config   CubeEgressConfig
	mu       sync.Mutex
	sessions map[string]*cubeEgressSession
}

// ConfigureCubeEgress runs before reconciliation/admission. No background work
// outlives the server context, and disabled deployments retain their old path.
func (s *Server) ConfigureCubeEgress(ctx context.Context, cfg CubeEgressConfig) error {
	u, err := url.Parse(cfg.BridgeURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/api/bridge" ||
		u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("Cube reverse bridge must be a fixed HTTPS /api/bridge URL")
	}
	if len(cfg.Policy.ProtectedPrefixes) == 0 || s.Cube == nil || s.CubeAgentRelayOrigin == "" || s.AgentProxyURL == "" {
		return errors.New("Cube reverse egress requires protected addresses and configured model relay")
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return errors.New("cannot inventory local addresses for reverse egress")
	}
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil && prefix.Addr().Is4() {
			cfg.Policy.ProtectedPrefixes = append(cfg.Policy.ProtectedPrefixes, netip.PrefixFrom(prefix.Addr(), 32))
		}
	}
	if s.cubeEgress != nil {
		return errors.New("Cube reverse egress already configured")
	}
	cfg.Policy.ProtectedDomains = append(append([]string(nil), cfg.Policy.ProtectedDomains...), u.Hostname())
	for _, raw := range []string{s.CubeAgentRelayOrigin, s.AgentProxyURL, s.CubeProxyURL} {
		if origin, err := url.Parse(raw); err == nil {
			if host := origin.Hostname(); strings.Contains(host, ".") && !strings.Contains(host, ":") {
				cfg.Policy.ProtectedDomains = append(cfg.Policy.ProtectedDomains, host)
			}
		}
	}
	if err := cfg.Policy.Validate(); err != nil {
		return err
	}
	// Revalidate after adding the controller's own interface addresses. An
	// explicit app route must never point back into protected infrastructure.
	encodedServices, err := json.Marshal(cfg.AppHTTPServices)
	if err != nil {
		return err
	}
	cfg.AppHTTPServices, err = egress.ParseHTTPServices(string(encodedServices), cfg.Policy)
	if err != nil {
		return err
	}
	s.cubeEgress = &cubeEgressManager{ctx: ctx, config: cfg, sessions: make(map[string]*cubeEgressSession)}
	return nil
}

func (s *Server) ensureCubeEgress(ctx context.Context, id string) error {
	m := s.cubeEgress
	if m == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	binding, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		return err
	}
	// Include the sealed credential identity so key/binding rotation revokes a
	// channel even when an operator retains the remote VM identifier.
	generation := cubeEgressGeneration(binding)
	m.mu.Lock()
	entry := m.sessions[id]
	if entry == nil || entry.generation != generation {
		if entry != nil {
			entry.cancel()
		}
		lifetime, cancel := context.WithCancel(m.ctx)
		entry = &cubeEgressSession{generation: generation, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
		m.sessions[id] = entry
		go s.runCubeEgress(lifetime, id, binding.RuntimeID, entry)
	}
	m.mu.Unlock()
	for {
		entry.mu.Lock()
		ready, connected := entry.ready, entry.connected
		entry.mu.Unlock()
		if connected {
			return nil
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return ctx.Err()
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-entry.done:
			return errors.New("Cube reverse egress session stopped")
		}
	}
}

func (s *Server) stopCubeEgress(id string) {
	if s.cubeEgress == nil {
		return
	}
	m := s.cubeEgress
	m.mu.Lock()
	if entry := m.sessions[id]; entry != nil {
		entry.cancel()
		delete(m.sessions, id)
	}
	m.mu.Unlock()
}

func (s *Server) runCubeEgress(ctx context.Context, id, runtimeID string, entry *cubeEgressSession) {
	m := s.cubeEgress
	defer func() {
		entry.cancel()
		close(entry.done)
		m.mu.Lock()
		if m.sessions[id] == entry {
			delete(m.sessions, id)
		}
		m.mu.Unlock()
	}()
	for ctx.Err() == nil {
		binding, err := s.Store.GetRuntimeBinding(ctx, id)
		sb, rowErr := s.Store.Get(ctx, id)
		if err != nil || rowErr != nil || binding.RuntimeID != runtimeID || sb.RuntimeProvider != "cube" || sb.Status == "stopped" {
			return
		}
		if cubeEgressGeneration(binding) != entry.generation {
			return
		}
		client := s.runtimeClientFor(id)
		conn, err := client.OpenEgressChannel(ctx)
		if err == nil {
			channelCtx, cancel := context.WithCancel(ctx)
			// Persisted deletion, pause or credential rotation revokes even an
			// otherwise idle long-lived tunnel. Never resume a VM from this loop.
			go func() {
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-channelCtx.Done():
						return
					case <-ticker.C:
						b, e := s.Store.GetRuntimeBinding(channelCtx, id)
						row, re := s.Store.Get(channelCtx, id)
						if e != nil || re != nil || row.RuntimeProvider != "cube" || row.Status == "stopped" {
							cancel()
							return
						}
						if cubeEgressGeneration(b) != entry.generation {
							cancel()
							return
						}
					}
				}
			}()
			entry.mu.Lock()
			entry.connected = true
			close(entry.ready)
			entry.mu.Unlock()
			_ = egress.RunHost(channelCtx, conn, egress.HostOptions{
				Identity: egress.Identity{SandboxID: id, Generation: entry.generation}, Policy: m.config.Policy,
				Services:     map[string]http.Handler{"model": s.cubeEgressModelHandler(), "bridge": http.HandlerFunc(s.cubeEgressBridge)},
				HTTPServices: s.cubeAppHTTPServices(channelCtx, id, entry.generation),
			})
			cancel()
			entry.mu.Lock()
			entry.connected = false
			entry.ready = make(chan struct{})
			entry.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Server) cubeAppHTTPServices(ctx context.Context, id, generation string) map[string]http.Handler {
	row, err := s.Store.Get(ctx, id)
	if err != nil || !row.AppID.Valid || s.cubeEgress == nil {
		return nil
	}
	appID := row.AppID.String
	services := make(map[string]http.Handler)
	for _, service := range s.cubeEgress.config.AppHTTPServices[appID] {
		handler := service.Handler()
		services[service.Address()] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := egress.SourceIdentity(r.Context())
			current, err := s.Store.Get(r.Context(), id)
			if !ok || identity.SandboxID != id || identity.Generation != generation || err != nil ||
				!current.AppID.Valid || current.AppID.String != appID || current.RuntimeProvider != "cube" || current.Status != "running" ||
				!s.cubeEgressIdentityActive(r.Context(), identity) {
				http.Error(w, "forbidden", 403)
				return
			}
			handler.ServeHTTP(w, r)
		})
	}
	return services
}

func (s *Server) cubeEgressModelHandler() http.Handler {
	mux := http.NewServeMux()
	handler := func(w http.ResponseWriter, r *http.Request) {
		identity, ok := egress.SourceIdentity(r.Context())
		if !ok || identity.SandboxID != r.PathValue("sandboxID") || !s.cubeEgressIdentityActive(r.Context(), identity) {
			http.Error(w, "forbidden", 403)
			return
		}
		s.cubeModelRelay(w, r)
	}
	mux.HandleFunc("POST /v1/cube-model/{sandboxID}/{taskID}/v1/messages", handler)
	mux.HandleFunc("POST /v1/cube-model/{sandboxID}/{taskID}/v1/messages/count_tokens", handler)
	return mux
}

func (s *Server) cubeEgressBridge(w http.ResponseWriter, r *http.Request) {
	identity, ok := egress.SourceIdentity(r.Context())
	if !ok || s.cubeEgress == nil || !s.cubeEgressIdentityActive(r.Context(), identity) || r.Method != http.MethodPost || r.URL.Path != "/api/bridge" ||
		r.URL.RawPath != "" || r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 1 {
		http.Error(w, "forbidden", 403)
		return
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !validCubeBridgeToken(token) {
		http.Error(w, "forbidden", 403)
		return
	}
	probe, cancelProbe := context.WithTimeout(r.Context(), 3*time.Second)
	status, err := s.runtimeClientFor(identity.SandboxID).Status(probe)
	cancelProbe()
	if err != nil || status.ActiveTask == nil {
		http.Error(w, "forbidden", 403)
		return
	}
	scope, err := s.Store.CubeModelScopeFor(r.Context(), status.ActiveTask.ID, identity.SandboxID)
	digest := sha256.Sum256([]byte(token))
	if err != nil || !scope.ExpiresAt.After(time.Now()) || subtle.ConstantTimeCompare(digest[:], scope.BridgeHash) != 1 {
		http.Error(w, "forbidden", 403)
		return
	}
	task, err := s.Store.GetTask(r.Context(), status.ActiveTask.ID)
	if err != nil || task.SandboxID != identity.SandboxID || task.Status != "running" {
		http.Error(w, "forbidden", 403)
		return
	}
	target, _ := url.Parse(s.cubeEgress.config.BridgeURL)
	bounded, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-bounded.Done():
				return
			case <-ticker.C:
				if _, err := s.Store.CubeModelScopeFor(bounded, status.ActiveTask.ID, identity.SandboxID); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	r = r.Clone(bounded)
	r.Body = http.MaxBytesReader(w, r.Body, cubeModelBodyLimit)
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 2 * time.Minute, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL = target
			p.Out.Host = target.Host
			p.Out.Header = http.Header{"Authorization": {"Bearer " + token}, "Content-Type": {"application/json"}, "Accept": {"application/json"}}
		},
		ModifyResponse: func(response *http.Response) error {
			headers := make(http.Header)
			for _, key := range []string{"Content-Type", "Retry-After", "Request-Id", "X-Request-Id"} {
				if value := response.Header.Get(key); value != "" {
					headers.Set(key, value)
				}
			}
			headers.Set("Cache-Control", "private, no-store")
			response.Header = headers
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				return errors.New("bridge redirect refused")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "bridge unavailable", 502) },
	}
	proxy.ServeHTTP(w, r)
}

// Hash an unambiguous encoding, including routing metadata and encrypted token
// identity. Rotation must change the source identity even if RuntimeID stays.
func cubeEgressGeneration(binding *store.RuntimeBinding) string {
	encoded, _ := json.Marshal(struct {
		RuntimeID, Domain, TemplateID string
		Ciphertext, Nonce             []byte
	}{binding.RuntimeID, binding.Domain, binding.TemplateID, binding.TokenCiphertext, binding.TokenNonce})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *Server) cubeEgressIdentityActive(ctx context.Context, identity egress.Identity) bool {
	if s.Store == nil || identity.SandboxID == "" || identity.Generation == "" {
		return false
	}
	binding, err := s.Store.GetRuntimeBinding(ctx, identity.SandboxID)
	return err == nil && cubeEgressGeneration(binding) == identity.Generation
}
