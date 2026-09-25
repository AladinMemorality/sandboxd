package egress

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, n, h string) ([]netip.Addr, error) {
	return f(ctx, n, h)
}
func testPolicy() Policy {
	return Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("65.108.225.153/32")}, ProtectedDomains: []string{"baarcha.tn"}, Resolver: resolverFunc(func(_ context.Context, n, h string) ([]netip.Addr, error) {
		if n != "ip4" {
			panic("unexpected DNS family")
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})}
}
func TestPolicy(t *testing.T) {
	p := testPolicy()
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "65.108.225.153", "192.168.0.1", "100.64.0.1", "198.18.0.1", "224.0.0.1", "0.0.0.0", "::1", "::ffff:93.184.216.34", "baarcha.tn", "a.baarcha.tn", "example.com@127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			if _, err := p.Destination(context.Background(), host, 443); err == nil {
				t.Fatal("permitted denied destination")
			}
		})
	}
	if got, err := p.Destination(context.Background(), "registry.npmjs.org", 443); err != nil || got != "93.184.216.34:443" {
		t.Fatalf("public DNS: %q %v", got, err)
	}
	if _, err := p.Destination(context.Background(), "example.com", 22); err == nil {
		t.Fatal("unapproved port")
	}
	p.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, nil
	})
	if _, err := p.Destination(context.Background(), "mixed.example", 443); err == nil {
		t.Fatal("mixed private DNS accepted")
	}
	p.ProtectedPrefixes = nil
	if _, err := p.Destination(context.Background(), "example.com", 443); err == nil {
		t.Fatal("missing inventory accepted")
	}
}

type fixture struct {
	g       *Guest
	control *httptest.Server
	proxy   *httptest.Server
	cancel  context.CancelFunc
	done    chan error
	opts    HostOptions
}

func startFixture(t *testing.T, opts HostOptions) *fixture {
	t.Helper()
	g, err := NewGuest(GuestOptions{Authenticate: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer fixture-control" }})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{g: g, control: httptest.NewServer(g.ChannelHandler()), proxy: httptest.NewServer(g.ProxyHandler()), opts: opts}
	f.attach(t)
	t.Cleanup(func() {
		f.cancel()
		g.Close()
		select {
		case <-f.done:
		case <-time.After(3 * time.Second):
			t.Error("host did not stop")
		}
		f.proxy.Close()
		f.control.Close()
	})
	return f
}
func (f *fixture) attach(t *testing.T) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.control.URL, "http"), http.Header{"Authorization": []string{"Bearer fixture-control"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	done := f.done
	go func() { done <- RunHost(ctx, conn, f.opts) }()
	readyCtx, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if err := f.g.WaitReady(readyCtx); err != nil {
		t.Fatal(err)
	}
}
func fixtureOptions(target string) HostOptions {
	return HostOptions{Identity: Identity{SandboxID: "sandbox-one", Generation: "generation-one"}, Policy: testPolicy(), DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "93.184.216.34:80" && addr != "93.184.216.34:443" {
			return nil, errors.New("non-pinned address")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", target)
	}}
}
func proxyClient(t *testing.T, f *fixture) *http.Client {
	t.Helper()
	u, _ := url.Parse(f.proxy.URL)
	tr := &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}
func TestHTTPAndCONNECTCycles(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "CONNECT"}[secure], func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "public.example" {
					t.Errorf("host = %q", r.Host)
				}
				w.Header().Set("Content-Type", "text/plain")
				io.WriteString(w, strings.Repeat("hello ", 30000))
			})
			var target *httptest.Server
			if secure {
				target = httptest.NewTLSServer(handler)
			} else {
				target = httptest.NewServer(handler)
			}
			defer target.Close()
			f := startFixture(t, fixtureOptions(target.Listener.Addr().String()))
			client := proxyClient(t, f)
			scheme := "http"
			if secure {
				scheme = "https"
			}
			for i := 0; i < MaxStreams+8; i++ {
				resp, err := client.Get(scheme + "://public.example/")
				if err != nil {
					t.Fatalf("cycle %d: %v", i, err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || len(body) != 180000 || resp.StatusCode != 200 {
					t.Fatalf("cycle %d: status=%d len=%d err=%v", i, resp.StatusCode, len(body), err)
				}
			}
		})
	}
}
func TestConcurrentOpens(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer target.Close()
	f := startFixture(t, fixtureOptions(target.Listener.Addr().String()))
	client := proxyClient(t, f)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				resp, err := client.Get("http://public.example/")
				if err != nil {
					t.Error(err)
					return
				}
				b, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || string(b) != "ok" {
					t.Errorf("%q %v", b, err)
				}
			}
		}()
	}
	wg.Wait()
}
func TestFixedServiceIdentityPathsAndHeaders(t *testing.T) {
	var calls atomic.Int32
	opts := fixtureOptions("")
	opts.Services = map[string]http.Handler{"model": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		id, ok := SourceIdentity(r.Context())
		if !ok || id != optsIdentity() {
			t.Error("lost source identity")
		}
		if r.Header.Get("X-Api-Key") != "task-token" || r.Header.Get("X-Baarcha-Bridge") != "bridge-token" {
			t.Error("lost capabilities")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("forwarded untrusted header")
		}
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
	})}
	f := startFixture(t, opts)
	service := httptest.NewServer(f.g.ServiceHandler("model"))
	defer service.Close()
	path := "/v1/cube-model/sandbox-one/01ARZ3NDEKTSV4RRFFQ69G5FAV/v1/messages"
	for _, tt := range []struct {
		method, path string
		status       int
	}{{"POST", path, 200}, {"POST", path + "?beta=true", 200}, {"GET", path, 403}, {"POST", strings.Replace(path, "sandbox-one", "sandbox-two", 1), 403}, {"POST", path + "?redirect=1", 403}, {"POST", "/api/bridge", 403}, {"POST", path + "/../messages", 403}} {
		req, _ := http.NewRequest(tt.method, service.URL+tt.path, strings.NewReader("{}"))
		req.Header.Set("X-Api-Key", "task-token")
		req.Header.Set("X-Baarcha-Bridge", "bridge-token")
		req.Header.Set("Cookie", "host-secret=bad")
		req.Header.Set("X-Forwarded-Host", "spoof")
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tt.status {
			t.Errorf("%s: got %d want %d", tt.path, resp.StatusCode, tt.status)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("callback calls: %d", calls.Load())
	}
}
func optsIdentity() Identity { return Identity{SandboxID: "sandbox-one", Generation: "generation-one"} }
func TestServiceStreamingAndSessionRevocation(t *testing.T) {
	cancelled := make(chan struct{})
	opts := fixtureOptions("")
	opts.Services = map[string]http.Handler{"bridge": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})}
	f := startFixture(t, opts)
	service := httptest.NewServer(f.g.ServiceHandler("bridge"))
	defer service.Close()
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Post(service.URL+"/api/bridge", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: ready\n" {
		t.Fatalf("stream not flushed: %q %v", line, err)
	}
	oldDone := f.done
	f.attach(t)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("old service not revoked")
	}
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("old host still running")
	}
}
func TestSlowStreamBackpressureAndDisconnect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer target.Close()
	f := startFixture(t, fixtureOptions(target.Listener.Addr().String()))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := f.g.open(ctx, "public", "public.example", 80)
	if err != nil {
		t.Fatal(err)
	}
	// A request body larger than the window must flow without overflowing a frame
	// queue. The other stream continues while this intentionally never-ending
	// partial HTTP request is idle.
	_, err = st.Write([]byte("POST / HTTP/1.1\r\nHost: public.example\r\nContent-Length: 1048576\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	client := proxyClient(t, f)
	resp, err := client.Get("http://public.example/")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f.cancel()
	select {
	case <-st.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("disconnected stream not cancelled")
	}
	b := make([]byte, 1)
	_, err = st.Read(b)
	if err == nil {
		t.Fatal("disconnected stream remained readable")
	}
}
func TestWindowDoesNotGrow(t *testing.T) {
	// Protocol-level fixed-ring test proves a non-reading stream stalls its sender
	// after 64KiB, without blocking another stream's writes.
	g, err := NewGuest(GuestOptions{Authenticate: func(*http.Request) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(g.ChannelHandler())
	defer server.Close()
	defer g.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var first *stream
	opened := make(chan *stream, 2)
	host := newSession(ctx, conn, func(st *stream, f frame) {
		_ = st.s.send(frame{Type: "opened", ID: st.id})
		opened <- st
		<-st.ctx.Done()
	})
	go host.run()
	defer func() { host.close(); <-host.done }()
	if err = g.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	guest, err := g.open(ctx, "public", "public.example", 80)
	if err != nil {
		t.Fatal(err)
	}
	first = <-opened
	writeDone := make(chan error, 1)
	go func() { _, err := guest.Write(bytes.Repeat([]byte{'x'}, StreamWindow*3)); writeDone <- err }()
	time.Sleep(50 * time.Millisecond)
	first.mu.Lock()
	used := first.used
	first.mu.Unlock()
	if used != StreamWindow {
		t.Fatalf("buffer=%d want%d", used, StreamWindow)
	}
	select {
	case err := <-writeDone:
		t.Fatalf("writer did not backpressure: %v", err)
	default:
	}
	second, err := g.open(ctx, "public", "other.example", 80)
	if err != nil {
		t.Fatal(err)
	}
	peer := <-opened
	if _, err = second.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 2)
	if _, err = io.ReadFull(peer, b); err != nil || string(b) != "ok" {
		t.Fatalf("other stream blocked: %q %v", b, err)
	}
	host.close()
	select {
	case err := <-writeDone:
		if err == nil {
			t.Error("blocked writer succeeded after disconnect")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked writer leaked")
	}
}
func TestChannelAuthentication(t *testing.T) {
	g, _ := NewGuest(GuestOptions{Authenticate: func(*http.Request) bool { return false }})
	s := httptest.NewServer(g.ChannelHandler())
	defer s.Close()
	defer g.Close()
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http"), nil)
	if err == nil || resp.StatusCode != 401 || g.Ready() {
		t.Fatal("unauthorized channel accepted")
	}
}

func TestCapacityAndProtocolRejection(t *testing.T) {
	for _, bad := range []string{`{"type":"surprise","id":1}`, `{"type":"credit","id":1,"credit":9223372036854775807}`, `{"type":"data","id":1,"data":"eA==","identity":"spoof"}`, `{"type":"open","id":1,"kind":"public","host":"public.example","port":80}`} {
		t.Run(bad, func(t *testing.T) {
			g, _ := NewGuest(GuestOptions{Authenticate: func(*http.Request) bool { return true }})
			server := httptest.NewServer(g.ChannelHandler())
			defer server.Close()
			defer g.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err = g.WaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			// Populate a pending guest stream so malformed credit targets a live ID.
			opened := make(chan error, 1)
			go func() { _, err := g.open(ctx, "public", "public.example", 80); opened <- err }()
			var request frame
			if err = conn.ReadJSON(&request); err != nil {
				t.Fatal(err)
			}
			if err = conn.WriteMessage(websocket.TextMessage, []byte(bad)); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-opened:
				if err == nil {
					t.Fatal("invalid frame accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("invalid frame failed to revoke channel")
			}
		})
	}
	g, _ := NewGuest(GuestOptions{Authenticate: func(*http.Request) bool { return true }})
	server := httptest.NewServer(g.ChannelHandler())
	defer server.Close()
	defer g.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	host := newSession(ctx, conn, func(st *stream, _ frame) { st.s.send(frame{Type: "opened", ID: st.id}); <-st.ctx.Done() })
	go host.run()
	defer func() { host.close(); <-host.done }()
	if err = g.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	var streams []*stream
	for i := 0; i < MaxStreams; i++ {
		st, err := g.open(ctx, "public", "public.example", 80)
		if err != nil {
			t.Fatal(err)
		}
		streams = append(streams, st)
	}
	if _, err = g.open(ctx, "public", "public.example", 80); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity not enforced: %v", err)
	}
	for _, st := range streams {
		st.Close()
	}
}

func TestDNSReauthorizationPinsNumericAddress(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer target.Close()
	opts := fixtureOptions(target.Listener.Addr().String())
	var private atomic.Bool
	var dials atomic.Int32
	originalDial := opts.DialContext
	opts.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) { dials.Add(1); return originalDial(ctx, n, a) }
	opts.Policy.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		if private.Load() {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	f := startFixture(t, opts)
	client := proxyClient(t, f)
	resp, err := client.Get("http://public.example/")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	private.Store(true)
	resp, err = client.Get("http://public.example/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 || dials.Load() != 1 {
		t.Fatalf("rebound DNS dialled: status=%d dials=%d", resp.StatusCode, dials.Load())
	}
}

func TestActualProxyAwareClients(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer target.Close()
	f := startFixture(t, fixtureOptions(target.Listener.Addr().String()))
	for _, name := range []string{"curl", "npm"} {
		t.Run(name, func(t *testing.T) {
			path, err := exec.LookPath(name)
			if err != nil {
				t.Skip(name + " is not installed")
			}
			args := []string{"--fail", "--max-time", "5", "http://public.example/"}
			if name == "npm" {
				args = []string{"ping", "--registry=http://public.example", "--fetch-retries=0", "--fetch-timeout=5000"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, path, args...)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "HTTP_PROXY=" + f.proxy.URL, "HTTPS_PROXY=" + f.proxy.URL, "http_proxy=" + f.proxy.URL, "https_proxy=" + f.proxy.URL, "NO_PROXY=", "no_proxy=", "NPM_CONFIG_USERCONFIG=/dev/null"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("client failed: %v %s", err, out)
			}
		})
	}
}

func TestEarlyServiceRejectionCancelsIncompleteUpload(t *testing.T) {
	opts := fixtureOptions("")
	opts.Services = map[string]http.Handler{"bridge": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "rejected", 403) })}
	f := startFixture(t, opts)
	service := httptest.NewServer(f.g.ServiceHandler("bridge"))
	defer service.Close()
	conn, err := net.Dial("tcp", service.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	io.WriteString(conn, "POST /api/bridge HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1000000\r\nContent-Type: application/json\r\n\r\n{")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 403 || !strings.Contains(string(body), "rejected") {
		t.Fatalf("early response: %d %q %v", resp.StatusCode, body, err)
	}
}
func TestFixedServiceRejectsAbsoluteURIAndNonLoopback(t *testing.T) {
	g, _ := NewGuest(GuestOptions{Authenticate: func(*http.Request) bool { return true }})
	defer g.Close()
	for _, tt := range []struct {
		url, remote string
		code        int
	}{{"http://public.example/api/bridge", "127.0.0.1:12345", 400}, {"/api/bridge", "10.0.0.1:12345", 403}} {
		r := httptest.NewRequest("POST", tt.url, nil)
		r.RemoteAddr = tt.remote
		w := httptest.NewRecorder()
		g.ServiceHandler("bridge").ServeHTTP(w, r)
		if w.Code != tt.code {
			t.Fatalf("got %d want%d", w.Code, tt.code)
		}
	}
}
func TestDuplicateStreamIDRevokesChannel(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer target.Close()
	f := startFixture(t, fixtureOptions(target.Listener.Addr().String()))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := f.g.open(ctx, "public", "public.example", 80)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.s.send(frame{Type: "open", ID: st.id, Kind: "public", Host: "public.example", Port: 80}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-st.ctx.Done():
	case <-ctx.Done():
		t.Fatal("duplicate stream ID not rejected")
	}
}

func TestPublicInformationalResponsesAndTruncation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/truncated" {
			conn, rw, _ := w.(http.Hijacker).Hijack()
			io.WriteString(rw, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n8\r\npartial\r\n")
			rw.Flush()
			conn.Close()
			return
		}
		w.WriteHeader(103)
		io.WriteString(w, "final response")
	}))
	defer target.Close()
	f := startFixture(t, fixtureOptions(target.Listener.Addr().String()))
	client := proxyClient(t, f)
	resp, err := client.Get("http://public.example/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || string(body) != "final response" {
		t.Fatalf("early hints lost response: %d %q %v", resp.StatusCode, body, err)
	}
	resp, err = client.Get("http://public.example/truncated")
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("truncated body reported success")
	}
}

func TestNativeNodeProxyAndLocalBypass(t *testing.T) {
	node := os.Getenv("CUBE_EGRESS_TEST_NODE")
	if node == "" {
		var err error
		node, err = exec.LookPath("node")
		if err != nil {
			t.Skip("Node is not installed")
		}
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	var major, minor int
	if _, err = fmt.Sscanf(strings.TrimSpace(string(version)), "v%d.%d", &major, &minor); err != nil {
		t.Fatal(err)
	}
	if major == 22 && minor < 21 || major == 23 || major < 22 {
		t.Skip("native environment proxy requires Node22.21+ or24.5+")
	}
	if major == 24 && minor < 5 {
		t.Skip("http/https environment proxy requires Node24.5+")
	}
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "HTTPS"}[secure], func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"ok":true}`)
			})
			var target *httptest.Server
			if secure {
				target = httptest.NewTLSServer(handler)
			} else {
				target = httptest.NewServer(handler)
			}
			defer target.Close()
			local := httptest.NewServer(handler)
			defer local.Close()
			opts := fixtureOptions(target.Listener.Addr().String())
			f := startFixture(t, opts)
			origin := "http://public.example"
			module := "node:http"
			if secure {
				origin = "https://example.com"
				module = "node:https"
			}
			script := `const [origin, local, moduleName] = process.argv.slice(1);
const a=await fetch(origin);if(!(await a.json()).ok)throw Error('native fetch failed');
const http=await import(moduleName);await new Promise((resolve,reject)=>{http.get(origin,r=>{let b='';r.on('data',c=>b+=c);r.on('error',reject);r.on('end',()=>{try{if(!JSON.parse(b).ok)throw Error('http body');resolve()}catch(e){reject(e)}})}).on('error',reject)});
const b=await fetch(local);if(!(await b.json()).ok)throw Error('local bypass failed');`
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, origin, local.URL, module)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "NODE_USE_ENV_PROXY=1", "HTTP_PROXY=" + f.proxy.URL, "HTTPS_PROXY=" + f.proxy.URL, "http_proxy=" + f.proxy.URL, "https_proxy=" + f.proxy.URL, "NO_PROXY=localhost,127.0.0.1", "no_proxy=localhost,127.0.0.1"}
			if secure {
				ca := filepath.Join(t.TempDir(), "fixture-ca.pem")
				if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.Certificate().Raw}), 0600); err != nil {
					t.Fatal(err)
				}
				cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS="+ca)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("native Node%s proxy failed: %v %s", strings.TrimSpace(string(version)), err, out)
			}
		})
	}
}

// A callback may return before consuming an upload. Reusing that HTTP/1
// connection after full-duplex cancellation must not poison the next request
// or race net/http's background body reader.
func TestEarlyServiceResponsesCloseHTTP1Connection(t *testing.T) {
	var calls atomic.Int32
	opts := fixtureOptions("")
	opts.Services = map[string]http.Handler{"bridge": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "rejected before upload completes", http.StatusForbidden)
	})}
	f := startFixture(t, opts)
	service := httptest.NewServer(f.g.ServiceHandler("bridge"))
	defer service.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for i := 0; i < 24; i++ {
		// Exceed reverse-channel credit to exercise cancellation with an upload
		// writer potentially blocked behind a callback which never reads the body.
		response, err := client.Post(service.URL+"/api/bridge", "application/json", strings.NewReader(strings.Repeat("x", StreamWindow*2)))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "rejected before upload") {
			t.Fatalf("request %d: status=%d body=%q error=%v", i, response.StatusCode, body, err)
		}
		if !response.Close {
			t.Fatalf("request %d: unsafe full-duplex HTTP/1 connection remains reusable", i)
		}
	}
	if calls.Load() != 24 {
		t.Fatalf("callback calls=%d", calls.Load())
	}
}
