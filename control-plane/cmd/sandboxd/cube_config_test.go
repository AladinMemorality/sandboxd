package main

import (
	"encoding/json"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

func TestCubeConfigRequiresExplicitPilot(t *testing.T) {
	t.Setenv("SANDBOXD_CUBE_ENABLED", "")
	cfg, err := loadCubeConfig()
	if err != nil || cfg.client != nil {
		t.Fatalf("Docker default lost: %+v %v", cfg, err)
	}
	t.Setenv("SANDBOXD_CUBE_ENABLED", "true")
	t.Setenv("SANDBOXD_CUBE_API_URL", "http://127.0.0.1:3000")
	t.Setenv("SANDBOXD_CUBE_API_KEY", "local-test-api-key")
	t.Setenv("SANDBOXD_CUBE_PROXY_URL", "http://127.0.0.1:80")
	t.Setenv("SANDBOXD_CUBE_DOMAIN", "cube.test")
	t.Setenv("SANDBOXD_CUBE_TEMPLATES", `{"react-vite":"tpl-reviewed"}`)
	t.Setenv("SANDBOXD_CUBE_APP_IDS", "")
	if _, err := loadCubeConfig(); err == nil {
		t.Fatal("allowed unrestricted rollout")
	}
	t.Setenv("SANDBOXD_CUBE_APP_IDS", "pilot-app")
	cfg, err = loadCubeConfig()
	if err != nil || !cfg.apps["pilot-app"] || cfg.templates["react-vite"] != "tpl-reviewed" {
		t.Fatalf("valid config: %+v %v", cfg, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"SANDBOXD_CUBE_PROXY_URL", "http://user:password@cube.test"},
		{"SANDBOXD_CUBE_PROXY_URL", "http://cube.test/path"},
		{"SANDBOXD_CUBE_DOMAIN", "cube.test/path"},
		{"SANDBOXD_CUBE_TEMPLATES", `{"untrusted-preset":"tpl-x"}`},
		{"SANDBOXD_CUBE_TEMPLATES", `{"react-vite":"../secrets"}`},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := loadCubeConfig(); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}

func TestCubeGlobalConfigRequiresCompleteExplicitDeployment(t *testing.T) {
	t.Setenv("SANDBOXD_CUBE_ENABLED", "true")
	t.Setenv("SANDBOXD_CUBE_ROLLOUT", "global")
	t.Setenv("SANDBOXD_CUBE_API_URL", "http://127.0.0.1:3000")
	t.Setenv("SANDBOXD_CUBE_API_KEY", "local-test-api-key")
	t.Setenv("SANDBOXD_CUBE_PROXY_URL", "http://127.0.0.1:80")
	t.Setenv("SANDBOXD_CUBE_DOMAIN", "cube.test")
	t.Setenv("SANDBOXD_CUBE_APP_IDS", "")
	t.Setenv("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS", "")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN", "https://relay.example")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "true")
	templates := map[string]string{}
	for _, p := range preset.List() {
		templates[p.ID] = "tpl-" + p.ID
	}
	encoded, _ := json.Marshal(templates)
	t.Setenv("SANDBOXD_CUBE_TEMPLATES", string(encoded))
	cfg, err := loadCubeConfig()
	if err != nil || !cfg.allApps || len(cfg.apps) != 0 {
		t.Fatalf("global mode unavailable: %+v %v", cfg, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"SANDBOXD_CUBE_ROLLOUT", "globla"},
		{"SANDBOXD_CUBE_APP_IDS", "one-app"},
		{"SANDBOXD_CUBE_TEMPLATES", `{"react-vite":"tpl-reviewed"}`},
		{"SANDBOXD_CUBE_AGENT_RELAY_ORIGIN", ""},
		{"SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "false"},
		{"SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS", "registry.npmjs.org"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := loadCubeConfig(); err == nil {
				t.Fatal("incomplete global deployment accepted")
			}
		})
	}
}

func TestCubeRelayRequiresHTTPSAndExplicitNetworkAttestation(t *testing.T) {
	t.Setenv("SANDBOXD_CUBE_ENABLED", "true")
	t.Setenv("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS", "")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN", "http://relay.example")
	if _, err := loadCubeConfig(); err == nil {
		t.Fatal("plain HTTP relay accepted")
	}
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN", "https://relay.example")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "")
	if _, err := loadCubeConfig(); err == nil {
		t.Fatal("unverified network relay enabled")
	}
}

func TestCubeReverseEgressRequiresExplicitCompatibleProfile(t *testing.T) {
	t.Setenv("SANDBOXD_CUBE_ENABLED", "true")
	t.Setenv("SANDBOXD_CUBE_API_URL", "http://127.0.0.1:3000")
	t.Setenv("SANDBOXD_CUBE_API_KEY", "fixture")
	t.Setenv("SANDBOXD_CUBE_PROXY_URL", "http://127.0.0.1:80")
	t.Setenv("SANDBOXD_CUBE_DOMAIN", "cube.test")
	t.Setenv("SANDBOXD_CUBE_TEMPLATES", `{"react-vite":"reviewed"}`)
	t.Setenv("SANDBOXD_CUBE_APP_IDS", "pilot-app")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN", "https://relay.example")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "true")
	t.Setenv("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS", "")
	t.Setenv("SANDBOXD_CUBE_ROLLOUT", "allowlist")
	t.Setenv("SANDBOXD_CUBE_REVERSE_EGRESS", "true")
	t.Setenv("SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE", "proxy-http-v1")
	t.Setenv("SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS", "65.108.225.153/32")
	t.Setenv("SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS", "baarcha.tn")
	t.Setenv("SANDBOXD_CUBE_BRIDGE_URL", "https://baarcha.tn/api/bridge")
	cfg, err := loadCubeConfig()
	if err != nil || cfg.reverseEgress == nil {
		t.Fatalf("reviewed pilot unavailable: %v", err)
	}
	for _, tt := range []struct{ k, v string }{{"SANDBOXD_CUBE_ENABLED", "false"}, {"SANDBOXD_CUBE_REVERSE_EGRESS", "1"}, {"SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE", "all-backends"}, {"SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS", ""}, {"SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS", "::/0"}, {"SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS", ""}, {"SANDBOXD_CUBE_BRIDGE_URL", "http://baarcha.tn/api/bridge"}, {"SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "false"}, {"SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS", "registry.npmjs.org"}} {
		t.Run(tt.k+tt.v, func(t *testing.T) {
			t.Setenv(tt.k, tt.v)
			if _, err := loadCubeConfig(); err == nil {
				t.Fatal("incomplete/unsupported configuration accepted")
			}
		})
	}
	// Expanding rollout does not change the network policy or its prerequisites.
	cfg.allApps = true
	if config, err := loadCubeReverseEgressConfig(cfg); err != nil || config == nil {
		t.Fatalf("reviewed global profile unavailable: %v", err)
	}
	t.Run("global retains network acceptance", func(t *testing.T) {
		t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "false")
		if _, err := loadCubeReverseEgressConfig(cfg); err == nil {
			t.Fatal("global rollout bypassed network acceptance")
		}
	})
	t.Setenv("SANDBOXD_CUBE_REVERSE_EGRESS", "false")
	if config, err := loadCubeReverseEgressConfig(cfg); err != nil || config != nil {
		t.Fatal("disabled path changed")
	}
}
