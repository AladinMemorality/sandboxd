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
	relayOrigin      string
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
	// Keep operator intent explicit and fail startup instead of silently
	// enabling a domain allowance that can override private-address isolation.
	if _, err := cube.OperatorEgressPolicy(os.Getenv("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS")); err != nil {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS: %w", err)
	}

	cfg.relayOrigin = strings.TrimRight(os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN"), "/")
	if cfg.relayOrigin != "" {
		origin, e := url.Parse(cfg.relayOrigin)
		if e != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			return cfg, fmt.Errorf("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN must be an HTTPS origin")
		}
		if os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED") != "true" {
			return cfg, fmt.Errorf("Cube model relay requires verified network isolation deployment attestation")
		}
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
