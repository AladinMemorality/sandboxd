package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestCubePiRequiresReviewedTemplateAndRelay(t *testing.T) {
	s, id, connections := cubeTaskFixture(t, func(http.ResponseWriter, *http.Request) { t.Fatal("invalid request reached guest") })
	body := `{"agent":"pi","model":"fixture-model","prompt":"build","env":{"BRIDGE_TOKEN":"project-bound-bridge-token-0123456789"}}`
	tests := []struct {
		name              string
		templates         map[string]bool
		relay, body, code string
		status            int
	}{
		{"disabled", nil, "", body, "agent_disabled", 400},
		{"old image", map[string]bool{"tpl-other": true}, "", body, "agent_unavailable", 409},
		{"missing relay", map[string]bool{"tpl-tasks": true}, "", body, "model_relay_disabled", 503},
		{"missing model", map[string]bool{"tpl-tasks": true}, "https://relay.test", `{"agent":"pi","prompt":"build"}`, "invalid_request", 400},
		{"missing bridge", map[string]bool{"tpl-tasks": true}, "https://relay.test", `{"agent":"pi","model":"test","prompt":"build"}`, "invalid_request", 400},
		{"forged relay", map[string]bool{"tpl-tasks": true}, "https://relay.test", `{"agent":"pi","model":"test","prompt":"build","env":{"RUNTIMED_CUBE_AGENT_TOKEN":"caller"}}`, "invalid_request", 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s.CubePiTemplates = tt.templates
			s.CubeAgentRelayOrigin = tt.relay
			s.AgentProxyURL = "http://proxy.test"
			w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/tasks", tt.body, cfgTenant)
			if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.code) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	if connections.Load() != 0 {
		t.Fatal("rejected Pi request woke a sandbox")
	}
}
