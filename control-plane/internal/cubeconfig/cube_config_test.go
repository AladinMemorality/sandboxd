package cubeconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

func TestCubeConfigRequiresExplicitPilot(t *testing.T) {
	t.Setenv("SANDBOXD_CUBE_ENABLED", "")
	cfg, err := Load()
	if err != nil || cfg.Client != nil {
		t.Fatalf("Docker default lost: %+v %v", cfg, err)
	}
	t.Setenv("SANDBOXD_PREVIEW_TOKEN_SECRETS", "fixture="+strings.Repeat("a", 32))
	t.Setenv("SANDBOXD_CUBE_ENABLED", "true")
	t.Setenv("SANDBOXD_CUBE_API_URL", "http://127.0.0.1:3000")
	t.Setenv("SANDBOXD_CUBE_API_KEY", "local-test-api-key")
	t.Setenv("SANDBOXD_CUBE_PROXY_URL", "http://127.0.0.1:80")
	t.Setenv("SANDBOXD_CUBE_DOMAIN", "cube.test")
	t.Setenv("SANDBOXD_CUBE_TEMPLATES", `{"react-vite":"tpl-reviewed"}`)
	t.Setenv("SANDBOXD_CUBE_APP_IDS", "")
	if _, err := Load(); err == nil {
		t.Fatal("allowed unrestricted rollout")
	}
	t.Setenv("SANDBOXD_CUBE_APP_IDS", "pilot-app")
	cfg, err = Load()
	if err != nil || !cfg.Apps["pilot-app"] || cfg.Templates["react-vite"] != "tpl-reviewed" {
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
			if _, err := Load(); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}

func TestCubeGlobalConfigRequiresCompleteExplicitDeployment(t *testing.T) {
	t.Setenv("SANDBOXD_PREVIEW_TOKEN_SECRETS", "fixture="+strings.Repeat("a", 32))
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
	Templates := map[string]string{}
	for _, p := range preset.List() {
		Templates[p.ID] = "tpl-" + p.ID
	}
	encoded, _ := json.Marshal(Templates)
	t.Setenv("SANDBOXD_CUBE_TEMPLATES", string(encoded))
	cfg, err := Load()
	if err != nil || !cfg.AllApps || len(cfg.Apps) != 0 {
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
			if _, err := Load(); err == nil {
				t.Fatal("incomplete global deployment accepted")
			}
		})
	}
}

func TestCubeRelayRequiresHTTPSAndExplicitNetworkAttestation(t *testing.T) {
	t.Setenv("SANDBOXD_PREVIEW_TOKEN_SECRETS", "fixture="+strings.Repeat("a", 32))
	t.Setenv("SANDBOXD_CUBE_ENABLED", "true")
	t.Setenv("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS", "")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN", "http://relay.example")
	if _, err := Load(); err == nil {
		t.Fatal("plain HTTP relay accepted")
	}
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN", "https://relay.example")
	t.Setenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "")
	if _, err := Load(); err == nil {
		t.Fatal("unverified network relay enabled")
	}
}

func TestCubeReverseEgressRequiresExplicitCompatibleProfile(t *testing.T) {
	t.Setenv("SANDBOXD_PREVIEW_TOKEN_SECRETS", "fixture="+strings.Repeat("a", 32))
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
	cfg, err := Load()
	if err != nil || cfg.ReverseEgress == nil {
		t.Fatalf("reviewed pilot unavailable: %v", err)
	}
	for _, tt := range []struct{ k, v string }{{"SANDBOXD_CUBE_ENABLED", "false"}, {"SANDBOXD_CUBE_REVERSE_EGRESS", "1"}, {"SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE", "all-backends"}, {"SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS", ""}, {"SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS", "::/0"}, {"SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS", ""}, {"SANDBOXD_CUBE_BRIDGE_URL", "http://baarcha.tn/api/bridge"}, {"SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED", "false"}, {"SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS", "registry.npmjs.org"}} {
		t.Run(tt.k+tt.v, func(t *testing.T) {
			t.Setenv(tt.k, tt.v)
			if _, err := Load(); err == nil {
				t.Fatal("incomplete/unsupported configuration accepted")
			}
		})
	}

	t.Run("Motion Studio explicit app scope", func(t *testing.T) {
		t.Setenv("SANDBOXD_CUBE_MOTION_STUDIO_APP_ID", "01M3CKN983PFRGMD711PCEPDFD")
		got, err := loadCubeReverseEgressConfig(cfg)
		if err != nil || got.MotionStudioAppID != "01M3CKN983PFRGMD711PCEPDFD" {
			t.Fatalf("fixed app configuration: %v", err)
		}
		for _, bad := range []string{"*", "all", "http://127.0.0.1", "01M3CKN983PFRGMD711PCEPDFD,OTHER", "01m3ckn983pfrgmd711pcepdfd"} {
			t.Setenv("SANDBOXD_CUBE_MOTION_STUDIO_APP_ID", bad)
			if _, err := loadCubeReverseEgressConfig(cfg); err == nil {
				t.Errorf("unsafe Motion app scope accepted: %q", bad)
			}
		}
	})
	t.Run("Cube preview signing is required", func(t *testing.T) {
		for _, value := range []string{"", "v1=", "malformed", "v1=short"} {
			t.Setenv("SANDBOXD_PREVIEW_TOKEN_SECRETS", value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SANDBOXD_PREVIEW_TOKEN_SECRETS") {
				t.Fatal("missing/invalid preview dependency accepted")
			}
		}
	})
	// Expanding rollout does not change the network policy or its prerequisites.
	cfg.AllApps = true
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
