package main

import (
	"fmt"
	"net/url"
	"os"

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
	// The reviewed Node 22 image opts native fetch into proxy routing. Bun and
	// raw database sockets still require separate compatibility proof, so this
	// path remains restricted to reviewed pilot apps; no flag overrides that gate.
	if cfg.allApps {
		return nil, fmt.Errorf("global reverse egress requires verified native-client and database compatibility; use reviewed pilot apps")
	}
	if cfg.relayOrigin == "" || os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED") != "true" {
		return nil, fmt.Errorf("reverse egress retains the model relay network isolation acceptance requirement")
	}
	if os.Getenv("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE") != "proxy-http-v1" {
		return nil, fmt.Errorf("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE must acknowledge the reviewed proxy-http-v1 pilot client limitations")
	}
	policy, err := egress.OperatorPolicy(os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS"), os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS"), os.Getenv("SANDBOXD_CUBE_API_URL"), cfg.proxyURL, cfg.relayOrigin, "https://"+cfg.domain)
	if err != nil {
		return nil, err
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
