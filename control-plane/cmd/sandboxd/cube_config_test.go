package main

import "testing"

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
