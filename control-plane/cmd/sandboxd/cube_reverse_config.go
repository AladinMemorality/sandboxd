package main

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/api"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
)

func loadCubeReverseEgressConfig(cfg cubeConfig) (*api.CubeEgressConfig, error) {
	switch os.Getenv("SANDBOXD_CUBE_REVERSE_EGRESS") {
	case "", "false":
		return nil, nil
	case "true":
	default:
		return nil, fmt.Errorf("SANDBOXD_CUBE_REVERSE_EGRESS must be true or false")
	}
	// Proxy variables do not make native fetch, Bun or database sockets work.
	// Until the packaged runtime has reviewed adapters, this path is restricted to
	// explicitly reviewed pilot apps. No boolean can override global compatibility.
	if cfg.allApps {
		return nil, fmt.Errorf("global reverse egress requires verified native-client and database compatibility; use reviewed pilot apps")
	}
	if cfg.relayOrigin == "" || os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED") != "true" {
		return nil, fmt.Errorf("reverse egress retains the model relay network isolation acceptance requirement")
	}
	if os.Getenv("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE") != "proxy-http-v1" {
		return nil, fmt.Errorf("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE must acknowledge the reviewed proxy-http-v1 pilot client limitations")
	}
	policy := egress.Policy{}
	raw := os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS")
	if len(raw) > 16384 || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("explicit bounded protected management CIDRs required")
	}
	for _, value := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || !prefix.Addr().Is4() {
			return nil, fmt.Errorf("protected CIDRs must be IPv4 prefixes")
		}
		policy.ProtectedPrefixes = append(policy.ProtectedPrefixes, prefix.Masked())
	}
	raw = os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS")
	if len(raw) > 32768 || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("explicit bounded protected management domains required")
	}
	for _, value := range strings.Split(raw, ",") {
		policy.ProtectedDomains = append(policy.ProtectedDomains, strings.TrimSpace(value))
	}
	policy.ProtectedDomains = append(policy.ProtectedDomains, cfg.domain)
	for _, rawURL := range []string{os.Getenv("SANDBOXD_CUBE_API_URL"), cfg.proxyURL, cfg.relayOrigin} {
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("invalid protected service origin")
		}
		if host := u.Hostname(); strings.Contains(host, ".") && !strings.Contains(host, ":") {
			policy.ProtectedDomains = append(policy.ProtectedDomains, host)
		}
	}
	bridge := os.Getenv("SANDBOXD_CUBE_BRIDGE_URL")
	u, err := url.Parse(bridge)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/api/bridge" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("SANDBOXD_CUBE_BRIDGE_URL must be a fixed HTTPS /api/bridge URL")
	}
	if err = policy.Validate(); err != nil {
		return nil, err
	}
	return &api.CubeEgressConfig{Policy: policy, BridgeURL: bridge}, nil
}
