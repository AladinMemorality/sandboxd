package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const cubePreviewTestID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
const cubePreviewTestHost = "s-" + cubePreviewTestID + "-3000.preview.example.test"

func cubePreviewJWT(t *testing.T, id, owner string, expiry time.Time) string {
	t.Helper()
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"preview"}`))
	b, _ := json.Marshal(auth.PreviewClaims{Aud: auth.PreviewAudience, Sub: owner, SandboxID: id, Exp: expiry.Unix()})
	p := h + "." + base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, []byte("preview-secret"))
	mac.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func cubePreviewFixture(t *testing.T, app http.HandlerFunc, passiveState ...string) (*Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	s, appID := newConfigTestServer(t)
	s.PreviewDomain = "example.test"
	s.PreviewURLScheme = "https"
	s.CubeDomain = "cube.test"
	s.Locks = idlock.New()
	s.Auth = auth.NewMiddleware(&auth.Config{PreviewSecrets: map[string]string{"preview": "preview-secret"}}, nil, nil, s.Log)
	connects := &atomic.Int32{}
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(passiveState) > 0 && r.Method == "GET" && r.URL.Path == "/sandboxes/vm-preview" && r.Header.Get("X-API-Key") == "management-secret" {
			if passiveState[0] == "error" {
				w.WriteHeader(503)
				return
			}
			json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-preview", TemplateID: "tpl-safe", State: passiveState[0], CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_id": cubePreviewTestID, "sandboxd_app_id": appID}})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/sandboxes/vm-preview/connect" || r.Header.Get("X-API-Key") != "management-secret" {
			t.Errorf("incorrect management request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		connects.Add(1)
		fmt.Fprint(w, `{"sandboxID":"vm-preview","templateID":"tpl-safe"}`)
	}))
	t.Cleanup(management.Close)
	var err error
	s.Cube, err = cube.New(cube.Config{APIURL: management.URL, APIKey: "management-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(passiveState) > 0 {
		ctx := context.Background()
		if err = s.Cube.ConfigureAdmission(ctx, s.Store, cube.AdmissionConfig{MaxActive: 4, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{"tpl-safe": {CPUCount: 2, MemoryMB: 2048}}}); err != nil {
			t.Fatal(err)
		}
		a, e := s.Store.AdmissionBegin(ctx, "app:"+appID, "", "tpl-safe", "create", "owned-operation")
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Store.AdmissionFinish(ctx, a, "vm-preview", "active"); e != nil {
			t.Fatal(e)
		}
	}
	guestCalls := &atomic.Int32{}
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "3031-vm-preview.cube.test" {
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) || r.Header.Get("Cube-Traffic-Access-Token") != "ingress-secret" {
				t.Error("supervisor credential mismatch")
			}
			fmt.Fprint(w, `{}`)
			return
		}
		guestCalls.Add(1)
		if r.Host != "3000-vm-preview.cube.test" || r.Header.Get("Cube-Traffic-Access-Token") != "ingress-secret" {
			t.Error("untrusted preview routing")
		}
		if strings.Contains(r.Header.Get("Authorization"), strings.Repeat("a", 64)) || strings.Contains(r.Header.Get("Authorization"), "management-secret") || r.Header.Get("X-API-Key") == "management-secret" {
			t.Error("management credential leaked")
		}
		app(w, r)
	}))
	t.Cleanup(guest.Close)
	s.CubeProxyURL = guest.URL
	plain, _ := json.Marshal(cubeCredentials{SupervisorToken: strings.Repeat("a", 64), TrafficAccessToken: "ingress-secret"})
	sealed, nonce, err := s.Secrets.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	sb := &store.Sandbox{ID: cubePreviewTestID, Status: "stopped", Visibility: "private", RuntimeProvider: "cube", Ports: []int{3000, 3031, 49983}, WebPort: sql.NullInt64{Int64: 3000, Valid: true}, ExternalUserID: sql.NullString{String: "owner-one", Valid: true}, AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-preview", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: sealed, TokenNonce: nonce}}
	if err = s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	return s, connects, guestCalls
}

func cubePreviewRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = cubePreviewTestHost
	r.AddCookie(&http.Cookie{Name: "sandbox_preview", Value: cubePreviewJWT(t, cubePreviewTestID, "owner-one", time.Now().Add(time.Hour))})
	return r
}

func TestCubePreviewResumesPreservesRequestAndScopesHeaders(t *testing.T) {
	s, connects, guestCalls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.RequestURI() != "/api/save?q=hello%20world" {
			t.Errorf("request changed %s %s", r.Method, r.URL.RequestURI())
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "original-body" {
			t.Error("lost POST body")
		}
		for _, key := range []string{"X-Sandbox-External-User-Id", "E2b-Traffic-Access-Token", "X-Forwarded-For", "Forwarded", "X-Cube-Request-Id"} {
			if r.Header.Get(key) != "" {
				t.Errorf("spoofed %s retained", key)
			}
		}
		if r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("X-Forwarded-Host") != cubePreviewTestHost {
			t.Error("bad trusted forwarding")
		}
		if r.Header.Get("Authorization") != "Bearer app-bearer-token" || r.Header.Get("X-API-Key") != "app-api-key" {
			t.Error("application authentication was stripped")
		}
		if len(r.Cookies()) != 1 || r.Cookies()[0].Name != "app_session" {
			t.Errorf("platform cookies leaked or app session lost: %v", r.Cookies())
		}
		w.Header().Set("Cube-Traffic-Access-Token", "ingress-secret")
		w.Header().Add("Set-Cookie", "sandbox_preview=attacker; Domain=.preview.example.test")
		w.Header().Add("Set-Cookie", "app_session=updated; Domain=.preview.example.test; HttpOnly")
		fmt.Fprint(w, "saved")
	})
	r := cubePreviewRequest(t, "POST", "/api/save?q=hello%20world", "original-body")
	r.Header.Set("Authorization", "Bearer app-bearer-token")
	r.Header.Set("X-API-Key", "app-api-key")
	r.Header.Set("Cube-Traffic-Access-Token", "attacker-token")
	r.Header.Set("E2b-Traffic-Access-Token", "attacker-token")
	r.Header.Set("X-Sandbox-External-User-Id", "other-owner")
	r.Header.Set("X-Cube-Request-Id", "spoof")
	r.Header.Set("X-Forwarded-Host", "s-other-3031.preview.evil.test")
	r.Header.Set("X-Forwarded-Proto", "http")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("Forwarded", "host=evil.test")
	r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "platform-session"})
	r.AddCookie(&http.Cookie{Name: "app_session", Value: "app-cookie"})
	w := httptest.NewRecorder()
	if !s.TryServeCubePreview(w, r) || w.Code != 200 || w.Body.String() != "saved" {
		t.Fatalf("preview %d %s", w.Code, w.Body.String())
	}
	if connects.Load() != 1 || guestCalls.Load() != 1 {
		t.Fatalf("wrong call counts %d %d", connects.Load(), guestCalls.Load())
	}
	if w.Header().Get("Cube-Traffic-Access-Token") != "" {
		t.Fatal("response leaked token")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "app_session" || cookies[0].Domain != "" {
		t.Fatalf("unsafe response cookies %+v", cookies)
	}
	sb, err := s.Store.Get(context.Background(), cubePreviewTestID)
	if err != nil || sb.Status != "running" {
		t.Fatalf("resume not persisted: %+v %v", sb, err)
	}
}

func TestCubePreviewRejectsWrongOwnerSandboxPortAndSpoofedHost(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unauthorized app request") })
	for _, tc := range []struct {
		name    string
		mutate  func(*http.Request)
		code    int
		handled bool
	}{
		{"no cookie", func(r *http.Request) { r.Header.Del("Cookie") }, 401, true},
		{"wrong owner", func(r *http.Request) {
			r.Header.Set("Cookie", "sandbox_preview="+cubePreviewJWT(t, cubePreviewTestID, "other-owner", time.Now().Add(time.Hour)))
		}, 401, true},
		{"wrong sandbox", func(r *http.Request) {
			r.Header.Set("Cookie", "sandbox_preview="+cubePreviewJWT(t, "OTHERID", "owner-one", time.Now().Add(time.Hour)))
		}, 401, true},
		{"expired", func(r *http.Request) {
			r.Header.Set("Cookie", "sandbox_preview="+cubePreviewJWT(t, cubePreviewTestID, "owner-one", time.Now().Add(-time.Hour)))
		}, 401, true},
		{"forged cookie", func(r *http.Request) { r.Header.Set("Cookie", "sandbox_preview=invalid") }, 401, true},
		{"supervisor", func(r *http.Request) { r.Host = strings.Replace(r.Host, "-3000.", "-3031.", 1) }, 404, true},
		{"unlisted port", func(r *http.Request) { r.Host = strings.Replace(r.Host, "-3000.", "-3001.", 1) }, 404, true},
		{"spoof forwarded host", func(r *http.Request) { r.Host = "evil.test"; r.Header.Set("X-Forwarded-Host", cubePreviewTestHost) }, 200, false},
		{"suffix host", func(r *http.Request) { r.Host += ".evil.test" }, 200, false},
		{"sibling origin", func(r *http.Request) { r.Header.Set("Origin", "https://s-other-3000.preview.example.test") }, 403, true},
		{"spoof origin proto", func(r *http.Request) {
			r.Header.Set("Origin", "http://"+r.Host)
			r.Header.Set("X-Forwarded-Proto", "http")
		}, 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := cubePreviewRequest(t, "GET", "/", "")
			tc.mutate(r)
			w := httptest.NewRecorder()
			handled := s.TryServeCubePreview(w, r)
			if handled != tc.handled || w.Code != tc.code {
				t.Fatalf("handled=%v code=%d", handled, w.Code)
			}
		})
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("rejected request woke/contacted guest")
	}
}

func TestCubePreviewLowercaseDNSAndRealWebSocket(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{Subprotocols: []string{"app-live"}, CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		if err = conn.WriteMessage(kind, append([]byte("hmr:"), payload...)); err != nil {
			t.Error(err)
		}
	})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.TryServeCubePreview(w, r) {
			http.NotFound(w, r)
		}
	}))
	defer front.Close()
	header := http.Header{}
	header.Set("Host", strings.ToLower(cubePreviewTestHost))
	header.Set("Origin", "https://"+strings.ToLower(cubePreviewTestHost))
	header.Set("Cookie", "sandbox_preview="+cubePreviewJWT(t, cubePreviewTestID, "owner-one", time.Now().Add(time.Hour)))
	dialer := websocket.Dialer{Subprotocols: []string{"app-live"}, HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/hmr", header)
	if err != nil {
		t.Fatalf("websocket: %v response=%v", err, resp)
	}
	defer conn.Close()
	if conn.Subprotocol() != "app-live" {
		t.Fatal("application subprotocol lost")
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.WriteMessage(websocket.TextMessage, []byte("reload")); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.ReadMessage()
	if err != nil || string(body) != "hmr:reload" {
		t.Fatalf("echo %q %v", body, err)
	}
	if connects.Load() != 1 || calls.Load() != 1 {
		t.Fatal("wrong websocket upstream calls")
	}
}

func TestCubePreviewFailsClosedOnMissingOwnerAndMismatchedRouting(t *testing.T) {
	for _, missingOwner := range []bool{false, true} {
		t.Run(fmt.Sprint(missingOwner), func(t *testing.T) {
			s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid binding reached app") })
			request := cubePreviewRequest(t, "GET", "/", "")
			if missingOwner {
				// A missing owner is never the legacy permissive owner-check fallback.
				sb, err := s.Store.Get(context.Background(), cubePreviewTestID)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Store.PurgeSandbox(context.Background(), cubePreviewTestID); err != nil {
					t.Fatal(err)
				}
				b, nonce, err := s.Secrets.Seal([]byte(`{"traffic_access_token":"ingress-secret"}`))
				if err != nil {
					t.Fatal(err)
				}
				sb.ExternalUserID = sql.NullString{}
				sb.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAA"
				sb.RuntimeBinding = &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-preview", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: b, TokenNonce: nonce}
				if err = s.Store.Create(context.Background(), sb); err != nil {
					t.Fatal(err)
				}
				request.Host = strings.Replace(cubePreviewTestHost, cubePreviewTestID, sb.ID, 1)
			} else {
				s.CubeDomain = "different.invalid"
			}
			w := httptest.NewRecorder()
			if !s.TryServeCubePreview(w, request) {
				t.Fatal("Cube fell through to Docker")
			}
			if w.Code != 403 && w.Code != 503 {
				t.Fatalf("unexpected %d", w.Code)
			}
			if connects.Load() != 0 || calls.Load() != 0 {
				t.Fatal("invalid binding triggered remote call")
			}
		})
	}
}

func TestCubePreviewRedirectKeepsStableHostAndPrivateCachePolicy(t *testing.T) {
	s, _, _ := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Location", "http://3000-vm-preview.cube.test/login?next=%2Fapp")
		w.WriteHeader(302)
	})
	w := httptest.NewRecorder()
	if !s.TryServeCubePreview(w, cubePreviewRequest(t, "GET", "/", "")) || w.Code != 302 {
		t.Fatalf("redirect response%d", w.Code)
	}
	if w.Header().Get("Location") != "https://"+cubePreviewTestHost+"/login?next=%2Fapp" {
		t.Fatalf("internal URL leaked into redirect %q", w.Header().Get("Location"))
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("authenticated preview could be publicly cached")
	}
}

func TestCubePreviewRejectsMalformedUpstreamAuthorities(t *testing.T) {
	for _, raw := range []string{"http://user:pass@cube.test", "http://cube.test/?token=x", "http://cube.test/override", "file:///tmp/private", "http://cube.test:99999", "http://cube.test/#fragment"} {
		if _, err := cubePreviewOrigin(raw); err == nil {
			t.Errorf("accepted origin %q", raw)
		}
	}
	for _, domain := range []string{"cube.test:3000", "cube.test/other", "cube.test?x=y", "cube.test@evil.test", "cube..test", "-evil.test"} {
		if _, err := cubePreviewHost(3000, "vm-good", domain); err == nil {
			t.Errorf("accepted domain %q", domain)
		}
	}
	for _, id := range []string{"../other", "vm/other", "vm?x=y", strings.Repeat("a", 60)} {
		if _, err := cubePreviewHost(3000, id, "cube.test"); err == nil {
			t.Errorf("accepted runtime ID %q", id)
		}
	}
}

func TestCubePreviewCookieNamesPreserveAppFrameworkSessions(t *testing.T) {
	for _, name := range []string{"app_session", "authjs.session-token", "__Secure-next-auth.session-token"} {
		if cubePlatformCookie(name) {
			t.Errorf("application session %q classified as platform", name)
		}
	}
	for _, name := range []string{"sandbox_preview", auth.SessionCookie} {
		if !cubePlatformCookie(name) {
			t.Errorf("platform cookie %q forwarded", name)
		}
	}
}

func TestCubePreviewAuthHandoffIsScopedAndNeverReachesGuest(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("handoff reached guest") })
	token := cubePreviewJWT(t, cubePreviewTestID, "owner-one", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest("GET", "/__sandboxd/preview-auth?"+url.Values{"token": {token}, "path": {"/app?q=ready"}}.Encode(), nil)
	r.Host = strings.ToLower(cubePreviewTestHost)
	w := httptest.NewRecorder()
	if !s.TryServeCubePreview(w, r) || w.Code != 302 || w.Header().Get("Location") != "/app?q=ready" {
		t.Fatalf("handoff%d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing preview cookie")
	}
	c := cookies[0]
	if c.Name != "sandbox_preview" || c.Value != token || c.Domain != "" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteNoneMode || c.MaxAge < 1 || c.MaxAge > 300 {
		t.Fatalf("unsafe cookie flags: name=%s domain=%s age=%d", c.Name, c.Domain, c.MaxAge)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || r.URL.RawQuery != "" || strings.Contains(w.Body.String(), token) {
		t.Fatal("handoff exposed capability")
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("handoff contacted runtime")
	}
}

func TestCubePreviewAuthHandoffRejectsCrossOwnerAndOpenRedirects(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid handoff reached guest") })
	valid := cubePreviewJWT(t, cubePreviewTestID, "owner-one", time.Now().Add(5*time.Minute))
	for _, tc := range []struct {
		token, path string
		code        int
	}{
		{cubePreviewJWT(t, cubePreviewTestID, "other-owner", time.Now().Add(time.Hour)), "/", 401},
		{cubePreviewJWT(t, "differentSandbox", "owner-one", time.Now().Add(time.Hour)), "/", 401},
		{cubePreviewJWT(t, cubePreviewTestID, "owner-one", time.Now().Add(-time.Minute)), "/", 401},
		{valid, "https://evil.test/", 400}, {valid, "//evil.test/", 400}, {valid, "/%2fevil.test", 400}, {valid, "/\\evil.test", 400},
		{valid, "/%09/evil.test", 400}, {valid, "/%5cevil.test", 400}, {valid, "/x/../__sandboxd/preview-auth", 400},
	} {
		r := httptest.NewRequest("GET", "/__sandboxd/preview-auth?"+url.Values{"token": {tc.token}, "path": {tc.path}}.Encode(), nil)
		r.Host = cubePreviewTestHost
		w := httptest.NewRecorder()
		if !s.TryServeCubePreview(w, r) || w.Code != tc.code || len(w.Result().Cookies()) != 0 {
			t.Errorf("accepted unsafe handoff path%q: %d", tc.path, w.Code)
		}
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("rejected handoff contacted runtime")
	}
}

func TestCubePreviewReadyAuthenticatesWithoutRunningGuest(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("preview-ready reached guest") })
	for _, authenticated := range []bool{false, true} {
		r := cubePreviewRequest(t, "GET", "/__sandboxd/preview-ready", "")
		want := http.StatusNoContent
		if !authenticated {
			r.Header.Del("Cookie")
			want = http.StatusUnauthorized
		}
		w := httptest.NewRecorder()
		if !s.TryServeCubePreview(w, r) || w.Code != want {
			t.Fatalf("ready authenticated=%v returned%d", authenticated, w.Code)
		}
		if authenticated && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("ready endpoint cacheable")
		}
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("preview-ready contacted runtime")
	}
	if !validCubePreviewReturnPath("/__sandboxd/preview-ready") {
		t.Fatal("refresh handoff target rejected")
	}
}

func TestCubePreviewRunningAssetsReuseLeaseAndFailuresDoNotReplay(t *testing.T) {
	var fail atomic.Bool
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "upstream unavailable", 503)
			return
		}
		fmt.Fprint(w, "ready")
	})
	request := func(method string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		if !s.TryServeCubePreview(w, cubePreviewRequest(t, method, "/asset.js", "payload")) || w.Code != want {
			t.Fatalf("preview %d %s", w.Code, w.Body.String())
		}
	}
	for i := 0; i < 6; i++ {
		request("GET", 200)
	}
	if connects.Load() != 1 || calls.Load() != 6 {
		t.Fatalf("asset requests repeated lifecycle probes: %d connects, %d app calls", connects.Load(), calls.Load())
	}
	s.cubePreviewLeases.Store(cubePreviewTestID, time.Now().Add(-time.Second))
	request("GET", 200)
	if connects.Load() != 2 {
		t.Fatal("expired lease not refreshed")
	}
	if err := s.Store.MarkStoppedAt(context.Background(), cubePreviewTestID, time.Now()); err != nil {
		t.Fatal(err)
	}
	request("GET", 200)
	if connects.Load() != 3 {
		t.Fatal("stopped runtime trusted a stale running lease")
	}
	fail.Store(true)
	before := calls.Load()
	request("POST", 503)
	if calls.Load() != before+1 || connects.Load() != 3 {
		t.Fatal("failed mutating request was replayed")
	}
	fail.Store(false)
	request("GET", 200)
	if connects.Load() != 4 {
		t.Fatal("upstream failure did not invalidate lease")
	}
}

func TestCubePreviewManagementPortsAlwaysForbidden(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("management port reached") })
	for _, port := range []int{3031, 49983} {
		if err := s.Store.SetWebPort(context.Background(), cubePreviewTestID, port); err != nil {
			t.Fatal(err)
		}
		r := cubePreviewRequest(t, "GET", "/", "")
		r.Host = strings.Replace(r.Host, "-3000.", fmt.Sprintf("-%d.", port), 1)
		w := httptest.NewRecorder()
		if !s.TryServeCubePreview(w, r) || w.Code != 404 {
			t.Fatalf("management port%d exposed", port)
		}
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("management request contacted guest")
	}
}
