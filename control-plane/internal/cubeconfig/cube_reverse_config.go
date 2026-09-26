package cubeconfig

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/api"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
)

func loadCubeReverseEgressConfig(cfg Config) (*api.CubeEgressConfig, error) {
	switch os.Getenv("SANDBOXD_CUBE_REVERSE_EGRESS") {
	case "", "false":
		return nil, nil
	case "true":
	default:
		return nil, fmt.Errorf("SANDBOXD_CUBE_REVERSE_EGRESS must be true or false")
	}
	// Rollout scope does not expand network capabilities. Every preset must use
	// the reviewed proxy client profile; direct guest NIC egress remains denied.
	if cfg.RelayOrigin == "" || os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED") != "true" {
		return nil, fmt.Errorf("reverse egress retains the model relay network isolation acceptance requirement")
	}
	if os.Getenv("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE") != "proxy-http-v1" {
		return nil, fmt.Errorf("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE must acknowledge the reviewed proxy-http-v1 client limitations")
	}
	policy, err := egress.OperatorPolicy(os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS"), os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS"), os.Getenv("SANDBOXD_CUBE_API_URL"), cfg.ProxyURL, cfg.RelayOrigin, "https://"+cfg.Domain)
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
	services, err := egress.ParseHTTPServices(os.Getenv("SANDBOXD_CUBE_APP_HTTP_SERVICES"), policy)
	if err != nil {
		return nil, err
	}
	motionApp := os.Getenv("SANDBOXD_CUBE_MOTION_STUDIO_APP_ID")
	if motionApp != "" {
		if _, err := egress.NewMotionStudio(motionApp, func(context.Context, egress.Identity, string) bool { return false }); err != nil {
			return nil, err
		}
	}
	return &api.CubeEgressConfig{Policy: policy, BridgeURL: bridge, AppHTTPServices: services, MotionStudioAppID: motionApp}, nil
}
