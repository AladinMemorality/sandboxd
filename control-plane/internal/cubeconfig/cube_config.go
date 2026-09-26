package cubeconfig

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/api"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

type Config struct {
	ReverseEgress    *api.CubeEgressConfig
	RelayOrigin      string
	Client           *cube.Client
	Templates        map[string]string
	Apps             map[string]bool
	AllApps          bool
	ProxyURL, Domain string
}

// Provider selection belongs to the operator, never the request body. Global
// mode selects Cube for new app sandboxes; it does not migrate existing rows.
// Disabling the flag doesn't reinterpret existing Cube rows as Docker.
func Load() (Config, error) {
	var cfg Config
	enabled := os.Getenv("SANDBOXD_CUBE_ENABLED")
	if enabled == "" || enabled == "false" {
		if value := os.Getenv("SANDBOXD_CUBE_REVERSE_EGRESS"); value != "" && value != "false" {
			return cfg, fmt.Errorf("reverse egress requires Cube enabled")
		}
		return cfg, nil
	}
	if enabled != "true" {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_ENABLED must be true or false")
	}
	if err := auth.ValidatePreviewSecrets(os.Getenv("SANDBOXD_PREVIEW_TOKEN_SECRETS")); err != nil {
		return cfg, err
	}
	switch os.Getenv("SANDBOXD_CUBE_ROLLOUT") {
	case "", "allowlist":
	case "global":
		cfg.AllApps = true
	default:
		return cfg, fmt.Errorf("SANDBOXD_CUBE_ROLLOUT must be allowlist or global")
	}
	// Keep operator intent explicit and fail startup instead of silently
	// enabling a domain allowance that can override private-address isolation.
	if _, err := cube.OperatorEgressPolicy(os.Getenv("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS")); err != nil {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS: %w", err)
	}

	cfg.RelayOrigin = strings.TrimRight(os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN"), "/")
	if cfg.RelayOrigin != "" {
		origin, e := url.Parse(cfg.RelayOrigin)
		if e != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			return cfg, fmt.Errorf("SANDBOXD_CUBE_AGENT_RELAY_ORIGIN must be an HTTPS origin")
		}
		if os.Getenv("SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED") != "true" {
			return cfg, fmt.Errorf("Cube model relay requires verified network isolation deployment attestation")
		}
	}
	cfg.ProxyURL = os.Getenv("SANDBOXD_CUBE_PROXY_URL")
	u, err := url.Parse(cfg.ProxyURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_PROXY_URL must be an administrator-controlled HTTP origin")
	}
	cfg.Domain = os.Getenv("SANDBOXD_CUBE_DOMAIN")
	if !regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`).MatchString(cfg.Domain) || strings.Contains(cfg.Domain, "..") {
		return cfg, fmt.Errorf("invalid SANDBOXD_CUBE_DOMAIN")
	}
	if err = json.Unmarshal([]byte(os.Getenv("SANDBOXD_CUBE_TEMPLATES")), &cfg.Templates); err != nil || len(cfg.Templates) == 0 {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_TEMPLATES must map trusted preset names to Cube template IDs")
	}
	for name, id := range cfg.Templates {
		if !preset.Valid(name) || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(id) {
			return cfg, fmt.Errorf("invalid Cube preset or template mapping")
		}
	}
	cfg.Apps = map[string]bool{}
	for _, id := range strings.Split(os.Getenv("SANDBOXD_CUBE_APP_IDS"), ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			cfg.Apps[id] = true
		}
	}
	if cfg.AllApps {
		if len(cfg.Apps) != 0 {
			return cfg, fmt.Errorf("global Cube rollout must not also specify SANDBOXD_CUBE_APP_IDS")
		}
		for _, p := range preset.List() {
			if cfg.Templates[p.ID] == "" {
				return cfg, fmt.Errorf("global Cube rollout requires a reviewed template for preset %s", p.ID)
			}
		}
		if cfg.RelayOrigin == "" {
			return cfg, fmt.Errorf("global Cube rollout requires the configured model relay")
		}
	} else if len(cfg.Apps) == 0 {
		return cfg, fmt.Errorf("SANDBOXD_CUBE_APP_IDS requires an explicit pilot app allowlist")
	}
	cfg.Client, err = cube.New(cube.Config{APIURL: os.Getenv("SANDBOXD_CUBE_API_URL"), APIKey: os.Getenv("SANDBOXD_CUBE_API_KEY")})
	if err != nil {
		return cfg, err
	}
	cfg.ReverseEgress, err = loadCubeReverseEgressConfig(cfg)
	return cfg, err
}
