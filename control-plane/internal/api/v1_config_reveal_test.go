package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
)

func TestAppConfigReveal(t *testing.T) {
	s, appID := newConfigTestServer(t)
	const value = "fixture-secret-only"
	do(s, "POST", "/v1/apps/"+appID+"/config", `{"key":"KEY","value":"`+value+`","sensitive":true}`, cfgTenant, map[string]string{"id": appID})
	reveal := func(tenant, kind, id, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/apps/"+id+"/config/"+key+"/reveal", nil)
		r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: tenant, Kind: kind}))
		r.SetPathValue("id", id)
		r.SetPathValue("key", key)
		w := httptest.NewRecorder()
		s.v1RevealAppConfig(w, r)
		return w
	}
	w := reveal(cfgTenant, "service", appID, "KEY")
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || body["value"] != value {
		t.Fatal("owner reveal failed")
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("reveal must not cache")
	}
	for _, c := range []struct {
		tenant, kind, id, key string
		status                int
	}{
		{"other-tenant", "service", appID, "KEY", 404},
		{cfgTenant, "service", "missing-app", "KEY", 404},
		{cfgTenant, "service", appID, "MISSING", 404},
		{"", "unknown", appID, "KEY", 403},
		{cfgTenant, "operator", appID, "KEY", 403},
	} {
		w := reveal(c.tenant, c.kind, c.id, c.key)
		if w.Code != c.status || strings.Contains(w.Body.String(), value) {
			t.Fatalf("unauthorized or missing reveal: got status %d, want %d", w.Code, c.status)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("errors must not cache")
		}
	}
	list := do(s, "GET", "/v1/apps/"+appID+"/config", "", cfgTenant, map[string]string{"id": appID})
	if strings.Contains(list.Body.String(), value) {
		t.Fatal("list must remain redacted after reveal")
	}
	s.Secrets = nil
	if w := reveal(cfgTenant, "service", appID, "KEY"); w.Code != 503 || strings.Contains(w.Body.String(), value) {
		t.Fatal("missing encryption must fail closed")
	}
}
