package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const serviceApp = "01M2NYBQS2HM74H5H378H6QG2D"

func exampleService() HTTPService {
	return HTTPService{Origin: "http://10.40.14.68:8080", Routes: map[string][]string{"GET": {"/health"}, "POST": {"/api/chat", "/api/tts", "/api/transcribe"}}}
}
func servicePolicy() Policy {
	return Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("10.0.2.0/24"), netip.MustParsePrefix("192.168.0.0/18")}}
}
func encodedService(s HTTPService) string {
	b, _ := json.Marshal(map[string][]HTTPService{serviceApp: {s}})
	return string(b)
}
func TestParseHTTPServicesScopes(t *testing.T) {
	for _, raw := range []string{"", "null", "{}"} {
		if got, e := ParseHTTPServices(raw, Policy{}); e != nil || len(got) != 0 {
			t.Fatalf("empty config %q: %v", raw, e)
		}
	}
	got, e := ParseHTTPServices(encodedService(exampleService()), servicePolicy())
	if e != nil || got[serviceApp][0].Address() != "10.40.14.68:8080" {
		t.Fatal(got, e)
	}
	for _, origin := range []string{"https://10.40.14.68:8080", "http://localhost:8080", "http://127.0.0.1:8080", "http://169.254.169.254:80", "http://65.108.225.153:443", "http://10.0.2.15:8080", "http://192.168.0.1:8080", "http://10.40.14.68:0", "http://10.40.14.68:08080", "http://10.40.14.68", "http://[::1]:8080", "http://u:p@10.40.14.68:8080", "http://10.40.14.68:8080/", "http://10.40.14.68:8080?", "http://10.40.14.68:8080/#x", "http://10.40.14.68:8080#"} {
		t.Run(origin, func(t *testing.T) {
			s := exampleService()
			s.Origin = origin
			if _, e := ParseHTTPServices(encodedService(s), servicePolicy()); e == nil {
				t.Fatal("unsafe origin accepted")
			}
		})
	}
	for _, route := range []string{"/api/../health", "//health", "/api%2fchat", "/api/chat?x=1", "/api/chat#x", "/api\\chat", "/api/chat/", "health"} {
		s := exampleService()
		s.Routes = map[string][]string{"GET": {route}}
		if _, e := ParseHTTPServices(encodedService(s), servicePolicy()); e == nil {
			t.Fatalf("unsafe route accepted %q", route)
		}
	}
	s := exampleService()
	s.Routes = map[string][]string{"CONNECT": {"/health"}}
	if _, e := ParseHTTPServices(encodedService(s), servicePolicy()); e == nil {
		t.Fatal("CONNECT configured")
	}
	s = exampleService()
	b, _ := json.Marshal(map[string][]HTTPService{serviceApp: {s, s}})
	if _, e := ParseHTTPServices(string(b), servicePolicy()); e == nil {
		t.Fatal("duplicate origin accepted")
	}
	if _, e := ParseHTTPServices(encodedService(s)+"{}", servicePolicy()); e == nil {
		t.Fatal("trailing configuration accepted")
	}
}

type serviceRoundTrip func(*http.Request) (*http.Response, error)

func (f serviceRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func serviceRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, Identity{SandboxID: "owned", Generation: "generation"}))
}
func TestHTTPServiceExactRoutesAndCredentials(t *testing.T) {
	var calls atomic.Int32
	s := exampleService()
	handler := s.handler(serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != "http://10.40.14.68:8080/api/chat" || r.Host != "10.40.14.68:8080" {
			t.Errorf("changed target %s host%s", r.URL, r.Host)
		}
		if r.Header.Get("Authorization") != "Bearer guest-capability" {
			t.Error("missing guest authorization")
		}
		for _, key := range []string{"Cookie", "X-Baarcha-Bridge", "X-Api-Key", "X-Forwarded-For", "Proxy-Authorization"} {
			if r.Header.Get(key) != "" {
				t.Errorf("leaked %s", key)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"host=secret"}, "Authorization": {"host-secret"}, "Www-Authenticate": {"sensitive"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	}))
	// Freeze config: later caller mutations cannot expand the constructed handler.
	s.Routes["POST"][0] = "/other"
	request := serviceRequest("POST", "/api/chat")
	request.Header.Set("Authorization", "Bearer guest-capability")
	for _, key := range []string{"Cookie", "X-Baarcha-Bridge", "X-Api-Key", "X-Forwarded-For", "Proxy-Authorization"} {
		request.Header.Set(key, "must-not-forward")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatal(w.Code, calls.Load())
	}
	for _, key := range []string{"Set-Cookie", "Authorization", "WWW-Authenticate"} {
		if w.Header().Get(key) != "" {
			t.Errorf("leaked response %s", key)
		}
	}
	for _, req := range []*http.Request{serviceRequest("GET", "/api/chat"), serviceRequest("POST", "/other"), serviceRequest("POST", "/api/chat?x=1"), serviceRequest("POST", "/api/%63hat"), serviceRequest("POST", "http://10.40.14.68:8080/api/chat"), serviceRequest("CONNECT", "/api/chat"), httptest.NewRequest("POST", "/api/chat", nil)} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Errorf("unsafe request status %d", w.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("denied request dialed upstream")
	}
}
func TestHTTPServiceRedirectAndBodyBounds(t *testing.T) {
	calls := 0
	handler := exampleService().handler(serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"http://127.0.0.1/admin"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, serviceRequest("GET", "/health"))
	if w.Code != 502 || w.Header().Get("Location") != "" || calls != 1 {
		t.Fatal("redirect was not blocked")
	}
	r := serviceRequest("POST", "/api/chat")
	r.ContentLength = maxHTTPServiceBody + 1
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 413 || calls != 1 {
		t.Fatal("oversized request forwarded")
	}
	for _, size := range []int{4, 5} {
		body := &boundedServiceBody{ReadCloser: io.NopCloser(bytes.NewReader(make([]byte, size))), remaining: 4}
		got, err := io.ReadAll(body)
		if len(got) != 4 || (size == 4 && err != nil) || (size == 5 && err == nil) {
			t.Fatalf("stream bound size%d got%d err%v", size, len(got), err)
		}
	}
}
func TestHTTPServiceCancellationReachesRealUpstream(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }))
	defer upstream.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != "10.40.14.68:8080" {
			t.Errorf("untrusted dial %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Listener.Addr().String())
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(serviceRequest("GET", "/health").Context())
	defer cancel()
	r := serviceRequest("GET", "/health").WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); exampleService().handler(transport).ServeHTTP(httptest.NewRecorder(), r) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upstream not reached")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream not cancelled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler not released")
	}
}
