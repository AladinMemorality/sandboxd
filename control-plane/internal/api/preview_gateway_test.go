package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewGatewayAuthorizesWithoutFetchingApp(t *testing.T) {
	s, _, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("app fetched by controller") }, "running")
	s.PreviewPublicDomain = "baarcha.tn"
	s.PreviewGatewayKeys = map[string]string{"vps": strings.Repeat("k", 32), "b200": strings.Repeat("b", 32)}
	original := cubePreviewRequest(t, "POST", "/save?x=1", "")
	original.Host = "s-" + cubePreviewTestID + "-3000.baarcha.tn"
	invoke := func(worker, key string, h http.Header, host string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(PreviewGatewayRequest{host, "POST", "/save?x=1", h})
		r := httptest.NewRequest("POST", "/preview-gateway", bytes.NewReader(b))
		r.Header.Set("X-Preview-Worker", worker)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.handlePreviewGateway(w, r)
		return w
	}
	good := invoke("vps", s.PreviewGatewayKeys["vps"], original.Header, original.Host)
	if good.Code != 200 {
		t.Fatalf("route: %d %s", good.Code, good.Body.String())
	}
	var out PreviewGatewayRoute
	if json.Unmarshal(good.Body.Bytes(), &out) != nil || out.Host != "3000-vm-preview.cube.test" || out.Token != "ingress-secret" || !out.Private {
		t.Fatal("incorrect route")
	}
	if calls.Load() != 0 {
		t.Fatal("application request escaped controller")
	}
	if w := invoke("vps", "wrong", original.Header, original.Host); w.Code != 401 {
		t.Fatal("invalid gateway accepted")
	}
	if w := invoke("b200", s.PreviewGatewayKeys["b200"], original.Header, original.Host); w.Code != 409 || strings.Contains(w.Body.String(), "ingress-secret") {
		t.Fatal("wrong worker received route")
	}
	if w := invoke("vps", s.PreviewGatewayKeys["vps"], nil, original.Host); w.Code == 200 || strings.Contains(w.Body.String(), "ingress-secret") {
		t.Fatal("unsigned private request accepted")
	}
	if w := invoke("vps", s.PreviewGatewayKeys["vps"], original.Header, strings.Replace(original.Host, "-3000.", "-3031.", 1)); w.Code != 404 {
		t.Fatal("supervisor exposed")
	}
	original.Header.Set("Origin", "https://s-other-3000.baarcha.tn")
	if w := invoke("vps", s.PreviewGatewayKeys["vps"], original.Header, original.Host); w.Code != 403 {
		t.Fatal("sibling origin accepted")
	}
}
