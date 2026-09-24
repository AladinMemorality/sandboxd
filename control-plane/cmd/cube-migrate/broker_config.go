package main

import (
	"fmt"
	"net/url"
	"os"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
)

// The offline tool uses the same explicit operator settings as sandboxd. It
// cannot attest a deployment, expand policy, or silently disable its broker.
func migrationBrokerPolicy() (egress.Policy, error) {
	empty := egress.Policy{}
	if os.Getenv("SANDBOXD_CUBE_REVERSE_EGRESS") != "true" {
		return empty, fmt.Errorf("offline migration requires configured SANDBOXD_CUBE_REVERSE_EGRESS=true")
	}
	if os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED") != "true" {
		return empty, fmt.Errorf("offline broker retains deployment network isolation acceptance requirement")
	}
	if os.Getenv("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE") != "proxy-http-v1" {
		return empty, fmt.Errorf("offline broker requires reviewed proxy-http-v1 client profile")
	}
	for _, key := range []string{"SANDBOXD_CUBE_API_URL", "SANDBOXD_CUBE_PROXY_URL"} {
		u, e := url.Parse(os.Getenv(key))
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return empty, fmt.Errorf("%s must be an explicit HTTP origin", key)
		}
	}
	relay, e := url.Parse(os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN"))
	if e != nil || relay.Scheme != "https" || relay.Host == "" || relay.User != nil || relay.Path != "" || relay.RawQuery != "" || relay.Fragment != "" {
		return empty, fmt.Errorf("configured HTTPS model relay origin required")
	}
	bridge, e := url.Parse(os.Getenv("SANDBOXD_CUBE_BRIDGE_URL"))
	if e != nil || bridge.Scheme != "https" || bridge.Host == "" || bridge.User != nil || bridge.Path != "/api/bridge" || bridge.RawPath != "" || bridge.RawQuery != "" || bridge.ForceQuery || bridge.Fragment != "" {
		return empty, fmt.Errorf("configured fixed HTTPS bridge URL required")
	}
	return egress.OperatorPolicy(os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS"), os.Getenv("SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS"), os.Getenv("SANDBOXD_CUBE_API_URL"), os.Getenv("SANDBOXD_CUBE_PROXY_URL"), relay.String(), bridge.String(), "https://"+os.Getenv("SANDBOXD_CUBE_DOMAIN"))
}

func migrationActionNeedsBroker(action string) bool {
	return action == "migrate" || action == "resume" || action == "rollback"
}
