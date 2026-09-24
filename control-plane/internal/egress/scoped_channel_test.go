package egress

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScopedHTTPChannelDoesNotGrantPrivateTCP(t *testing.T) {
	var calls, dials atomic.Int32
	opts := fixtureOptions("")
	opts.DialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("fixture must not dial any address")
	}
	opts.HTTPServices = map[string]http.Handler{"10.40.14.68:8080": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		identity, ok := SourceIdentity(r.Context())
		if !ok || identity != opts.Identity || r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("source identity or header isolation lost")
		}
		if r.Header.Get("Authorization") != "Bearer scoped-app-key" {
			t.Error("application credential lost")
		}
		io.WriteString(w, "scoped response")
	})}
	f := startFixture(t, opts)
	client := proxyClient(t, f)
	request, _ := http.NewRequest("GET", "http://10.40.14.68:8080/health", nil)
	request.Header.Set("Authorization", "Bearer scoped-app-key")
	request.Header.Set("Cookie", "platform=private")
	request.Header.Set("X-Forwarded-Host", "untrusted")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "scoped response" {
		t.Fatal("scoped HTTP request failed")
	}
	for _, target := range []string{
		"http://10.40.14.68:8081/health", "http://10.40.14.69:8080/health",
		"http://10.40.14.68:8080/health?redirect=elsewhere",
		"http://10.40.14.68:8080/%68ealth", "https://10.40.14.68:8080/health",
	} {
		response, err := client.Get(target)
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == 200 {
				t.Errorf("unauthorized target accepted: %s", target)
			}
		}
	}
	if calls.Load() != 1 || dials.Load() != 0 {
		t.Fatalf("private raw dialing or callback bypass: calls=%d dials=%d", calls.Load(), dials.Load())
	}
	// A second authenticated sandbox does not inherit this app's mapping.
	other := fixtureOptions("")
	other.Identity = Identity{SandboxID: "other", Generation: "other-generation"}
	other.DialContext = opts.DialContext
	foreign := startFixture(t, other)
	response, err = proxyClient(t, foreign).Get("http://10.40.14.68:8080/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == 200 || calls.Load() != 1 || dials.Load() != 0 {
		t.Fatal("foreign sandbox inherited app route")
	}
}

func TestScopedHTTPCallbackRejectsMethods(t *testing.T) {
	for _, method := range []string{"CONNECT", "DELETE", "PUT"} {
		var called atomic.Bool
		opts := fixtureOptions("")
		opts.HTTPServices = map[string]http.Handler{"10.40.14.68:8080": http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) })}
		f := startFixture(t, opts)
		request, _ := http.NewRequest(method, "http://10.40.14.68:8080/health", strings.NewReader("bounded"))
		response, err := proxyClient(t, f).Do(request)
		if err == nil {
			response.Body.Close()
		}
		if called.Load() {
			t.Errorf("method %s reached private service", method)
		}
	}
}

func TestScopedHTTPChannelParsesHTTPInsideConnect(t *testing.T) {
	// Node's proxy agent uses CONNECT even for a plain HTTP origin. Accept the
	// parsed HTTP request inside that stream, never relay the stream as TCP.
	opts := fixtureOptions("")
	var calls atomic.Int32
	opts.HTTPServices = map[string]http.Handler{"10.40.14.68:8080": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/health" {
			t.Error("wrong parsed request")
		}
		io.WriteString(w, "http-only")
	})}
	f := startFixture(t, opts)
	u, _ := url.Parse(f.proxy.URL)
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(conn, "CONNECT 10.40.14.68:8080 HTTP/1.1\r\nHost: 10.40.14.68:8080\r\n\r\n")
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT fixture: %v", err)
	}
	io.WriteString(conn, "GET /health HTTP/1.1\r\nHost: 10.40.14.68:8080\r\nConnection: close\r\n\r\n")
	response, err = http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "http-only" || calls.Load() != 1 {
		t.Fatal("HTTP callback inside CONNECT failed")
	}
}
