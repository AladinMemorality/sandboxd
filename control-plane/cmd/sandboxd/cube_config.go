package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

type cubeConfig struct {
	client           *cube.Client
	templates        map[string]string
	apps             map[string]bool
	proxyURL, domain string
}

// Cube remains a per-app operator-selected pilot, never a request-body runtime
// switch. Disabling the flag doesn't reinterpret existing Cube rows as Docker.
func loadCubeConfig() (cubeConfig, error) {
	var cfg cubeConfig
	enabled := os.Getenv("SANDBOXD_CUBE_ENABLED")
	if enabled == "" || enabled == "false" {
		return cfg, nil
	}
	if enabled != "true" {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_ENABLED must be true or false")
	}
	cfg.proxyURL = os.Getenv("SANDBOXD_CUBE_PROXY_URL")
	u, err := url.Parse(cfg.proxyURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_PROXY_URL must be an administrator-controlled HTTP origin")
	}
	cfg.domain = os.Getenv("SANDBOXD_CUBE_DOMAIN")
	if !regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`).MatchString(cfg.domain) || strings.Contains(cfg.domain, "..") {
		return cfg, fmt.Errorf("invalid SANDBOXD_CUBE_DOMAIN")
	}
	if err = json.Unmarshal([]byte(os.Getenv("SANDBOXD_CUBE_TEMPLATES")), &cfg.templates); err != nil || len(cfg.templates) == 0 {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_TEMPLATES must map trusted preset names to Cube template IDs")
	}
	for name, id := range cfg.templates {
		if !preset.Valid(name) || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(id) {
			return cfg, fmt.Errorf("invalid Cube preset or template mapping")
		}
	}
	cfg.apps = map[string]bool{}
	for _, id := range strings.Split(os.Getenv("SANDBOXD_CUBE_APP_IDS"), ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			cfg.apps[id] = true
		}
	}
	if len(cfg.apps) == 0 {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_APP_IDS requires an explicit pilot app allowlist")
	}
	cfg.client, err = cube.New(cube.Config{APIURL: os.Getenv("SANDBOXD_CUBE_API_URL"), APIKey: os.Getenv("SANDBOXD_CUBE_API_KEY")})
	return cfg, err
}
