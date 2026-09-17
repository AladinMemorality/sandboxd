package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

const controlTestToken = "01ab23cd45ef678901ab23cd45ef678901ab23cd45ef678901ab23cd45ef6789abcd"

func TestControlHTTPRequiresAuthBeforeRouting(t *testing.T) {
	a := &app{log: slog.Default(), bootedAt: time.Now()}
	handler := authenticatedControl(controlTestToken, a.controlHandler())
	for _, path := range []string{"/status", "/tasks", "/tasks/secret/events", "/tasks/secret/cancel", "/not-a-route"} {
		for _, token := range []string{"", "Bearer invalid", "Bearer " + controlTestToken + "x", "Basic " + controlTestToken} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", token)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusUnauthorized || res.Body.String() != "unauthorized\n" {
				t.Fatalf("%s leaked a route: %d %q", path, res.Code, res.Body.String())
			}
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer "+controlTestToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("authorized status: %d", res.Code)
	}
	req.Header.Add("Authorization", "Bearer "+controlTestToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatal("accepted ambiguous duplicate Authorization")
	}
}

func TestServeControlDefaultUnixAndCancellation(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "sock")
	a := &app{log: slog.Default(), bootedAt: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, socket, a) }()
	waitForControlStatus(t, runtime.NewClient(socket))
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Unix server did not stop")
	}
	if _, err := runtime.NewClient(socket).Status(context.Background()); err == nil {
		t.Fatal("Unix server still reachable")
	}
}

func TestServeControlRemoteAndUnixTogether(t *testing.T) {
	// Reserve an OS-assigned port before starting both production listeners.
	reserve, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserve.Addr().String()
	reserve.Close()
	socket := filepath.Join(t.TempDir(), "sock")
	a := &app{log: slog.Default(), bootedAt: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveControl(ctx, socket, a, remoteControl{Address: address, Token: controlTestToken}) }()
	c, err := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: "http://" + address, Token: controlTestToken})
	if err != nil {
		t.Fatal(err)
	}
	waitForControlStatus(t, c)
	waitForControlStatus(t, runtime.NewClient(socket))
	resp, err := http.Get("http://" + address + "/status")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("HTTP listener accepted unauthenticated request")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dual listener did not stop")
	}
}

func TestServeControlRejectsMissingToken(t *testing.T) {
	err := serveControl(context.Background(), filepath.Join(t.TempDir(), "sock"), &app{}, remoteControl{Address: "127.0.0.1:0"})
	if err == nil {
		t.Fatal("TCP listener accepted missing token")
	}
	if err := (remoteControl{}).validate(); err != nil {
		t.Fatal("Unix-only needs no token", err)
	}
}

func waitForControlStatus(t *testing.T, c *runtime.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		if _, err := c.Status(ctx); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("control listener did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
