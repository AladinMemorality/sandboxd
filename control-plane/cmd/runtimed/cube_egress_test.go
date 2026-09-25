package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
)

func TestReverseEgressGuestExplicitFlagAndAuthentication(t *testing.T) {
	t.Setenv("RUNTIMED_CUBE_REVERSE_EGRESS", "0")
	g, err := reverseEgressGuest(remoteControl{})
	if err != nil || g != nil {
		t.Fatal("disabled guest changed")
	}
	t.Setenv("RUNTIMED_CUBE_REVERSE_EGRESS", "true")
	if _, err = reverseEgressGuest(remoteControl{}); err == nil {
		t.Fatal("invalid flag accepted")
	}
	t.Setenv("RUNTIMED_CUBE_REVERSE_EGRESS", "1")
	t.Setenv("RUNTIMED_CUBE_GUEST", "")
	if _, err = reverseEgressGuest(remoteControl{Address: ":3031", Token: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("Docker enabled reverse channel")
	}
	t.Setenv("RUNTIMED_CUBE_GUEST", "1")
	for _, entry := range cubeProxyEnvironment() {
		key, _, _ := strings.Cut(entry, "=")
		t.Setenv(key, "")
	}
	g, err = reverseEgressGuest(remoteControl{Address: ":3031", Token: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	server := httptest.NewServer(g.ChannelHandler())
	defer server.Close()
	for _, token := range []string{"", "Bearer wrong"} {
		_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Authorization": {token}})
		if err == nil || resp.StatusCode != 401 {
			t.Fatal("channel accepted wrong bearer")
		}
	}
}
func TestCubeProxyMuxSupportsConnectAndFixedServices(t *testing.T) {
	g, _ := egress.NewGuest(egress.GuestOptions{Authenticate: func(*http.Request) bool { return true }})
	defer g.Close()
	control := httptest.NewServer(g.ChannelHandler())
	defer control.Close()
	proxy := httptest.NewServer(cubeProxyHandler(g))
	defer proxy.Close()
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "TLS fixture") }))
	defer target.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(control.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: "cube-one", Generation: "fresh"}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("65.108.225.153/32")}}, DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
			if a != "93.184.216.34:443" {
				t.Errorf("unexpected destination %q", a)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", target.Listener.Addr().String())
		}, Services: map[string]http.Handler{"bridge": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/bridge" {
				t.Error("incorrect bridge rewrite")
			}
			io.WriteString(w, "bridge")
		}), "model": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "model") })}})
	}()
	defer func() {
		cancel()
		g.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("channel leaked")
		}
	}()
	// Dial can observe the upgrade response before ChannelHandler registers the
	// guest session. Match application startup's readiness barrier instead of
	// racing the first CONNECT against that registration.
	readyCtx, stopReady := context.WithTimeout(ctx, time.Second)
	defer stopReady()
	if err := g.WaitReady(readyCtx); err != nil {
		t.Fatalf("reverse channel not ready: %v", err)
	}
	u, _ := url.Parse(proxy.URL)
	tr := &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	resp, err := client.Get("https://93.184.216.34/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "TLS fixture" {
		t.Fatalf("CONNECT mux failed: %q %v", body, err)
	}
	for path, want := range map[string]string{"/__cube/bridge": "bridge", "/__cube/model/v1/cube-model/cube-one/01ARZ3NDEKTSV4RRFFQ69G5FAV/v1/messages": "model"} {
		resp, err := http.Post(proxy.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != want {
			t.Fatalf("service routing: %q %v", body, err)
		}
	}
}

func TestCubeAppStartWaitsForAuthenticatedChannelAndCancels(t *testing.T) {
	guest, _ := egress.NewGuest(egress.GuestOptions{Authenticate: func(*http.Request) bool { return true }})
	defer guest.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	started := make(chan struct{}, 1)
	go func() {
		superviseAfterCubeEgress(ctx, guest, func(context.Context) { started <- struct{}{} })
		close(done)
	}()
	select {
	case <-started:
		t.Fatal("app started before channel")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startup wait ignored cancellation")
	}
	superviseAfterCubeEgress(context.Background(), nil, func(context.Context) { started <- struct{}{} })
	select {
	case <-started:
	default:
		t.Fatal("Docker startup changed")
	}
}
