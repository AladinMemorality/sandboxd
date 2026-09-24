package main

import (
	"testing"
)

func TestMigrationBrokerRequiresExplicitReviewedSettings(t *testing.T) {
	env := map[string]string{"SANDBOXD_CUBE_REVERSE_EGRESS": "true", "SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED": "true", "SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE": "proxy-http-v1", "SANDBOXD_CUBE_ROLLOUT": "allowlist", "SANDBOXD_CUBE_AGENT_RELAY_ORIGIN": "https://relay.example.com", "SANDBOXD_CUBE_BRIDGE_URL": "https://platform.example.com/api/bridge", "SANDBOXD_CUBE_API_URL": "https://api.example.com", "SANDBOXD_CUBE_PROXY_URL": "https://proxy.example.com", "SANDBOXD_CUBE_DOMAIN": "cube.example.com", "SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS": "192.0.2.2/32", "SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS": "management.example.com"}
	for k, v := range env {
		t.Setenv(k, v)
	}
	if _, e := migrationBrokerPolicy(); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"SANDBOXD_CUBE_REVERSE_EGRESS", "SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE", "SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS", "SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS", "SANDBOXD_CUBE_API_URL", "SANDBOXD_CUBE_PROXY_URL"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			if _, e := migrationBrokerPolicy(); e == nil {
				t.Fatal("missing setting accepted")
			}
		})
	}
	t.Setenv("SANDBOXD_CUBE_ROLLOUT", "global")
	if _, e := migrationBrokerPolicy(); e == nil {
		t.Fatal("global gate bypassed")
	}
}

func TestMigrationRecoveryActionsDoNotRequireBroker(t *testing.T) {
	for _, a := range []string{"abort", "adopt", "retire-source", "inventory", "status", "rollback-check"} {
		if migrationActionNeedsBroker(a) {
			t.Fatalf("%s incorrectly needs broker", a)
		}
	}
	for _, a := range []string{"migrate", "resume", "rollback"} {
		if !migrationActionNeedsBroker(a) {
			t.Fatalf("%s bypasses broker", a)
		}
	}
}
