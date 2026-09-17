package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testRemoteToken = "01ab23cd45ef678901ab23cd45ef678901ab23cd45ef678901ab23cd45ef6789abcd"

func TestRemoteClientRoutesProtocolAndAuthenticates(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Host != "3031-guest.cube.app" || r.Header.Get("Authorization") != "Bearer "+testRemoteToken || r.Header.Get("cube-traffic-access-token") != "private-ingress-token" {
			t.Errorf("missing routing or authorization headers")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(Status{Preview: PreviewState{Status: PreviewReady}})
		case "/tasks":
			w.WriteHeader(http.StatusAccepted)
		case "/tasks/task-one/messages":
			w.WriteHeader(http.StatusAccepted)
		case "/tasks/task-one/cancel", "/tasks/task-one/revert":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: testRemoteToken, Host: "3031-guest.cube.app", TrafficAccessToken: "private-ingress-token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if s, err := c.Status(ctx); err != nil || s.Preview.Status != PreviewReady {
		t.Fatalf("status: %v %v", s, err)
	}
	if err := c.StartTask(ctx, StartTaskRequest{TaskID: "task-one", Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendTaskMessage(ctx, "task-one", TaskMessage{Prompt: "continue"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelTask(ctx, "task-one"); err != nil {
		t.Fatal(err)
	}
	if err := c.RevertTask(ctx, "task-one"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 5 {
		t.Fatalf("requests = %d", requests.Load())
	}
	if c.SocketPath() != "" {
		t.Fatal("remote client has a socket path")
	}
}

func TestRemoteClientDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: redirect.URL, Token: testRemoteToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(context.Background()); err == nil {
		t.Fatal("expected redirect rejection")
	}
	if _, err := c.TaskEvents(context.Background(), "task-one", 0); err == nil {
		t.Fatal("expected stream redirect rejection")
	}
	if reached.Load() {
		t.Fatal("redirect target received a request")
	}
}

func TestRemoteStreamFlushesAndCancels(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/task-one/events" || r.URL.Query().Get("since") != "7" {
			t.Error("stream route lost")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, "{\"id\":7,\"type\":\"message\"}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: testRemoteToken})
	if err != nil {
		t.Fatal(err)
	}
	if c.stream.Timeout != 0 {
		t.Fatal("stream has an overall timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := c.TaskEvents(ctx, "task-one", 7)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var event Event
	if err := json.NewDecoder(stream).Decode(&event); err != nil || event.ID != 7 {
		t.Fatalf("first event: %+v, %v", event, err)
	}
	cancel()
	if _, err := io.ReadAll(stream); err == nil {
		t.Fatal("expected cancellation error")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("server did not receive cancellation")
	}
}

func TestRemoteConfigurationRejected(t *testing.T) {
	for _, base := range []string{"file:///tmp/sock", "http://user:password@localhost", "http://localhost/path", "http://localhost?q=secret", "http://localhost#fragment", "http://localhost?"} {
		if _, err := NewRemoteClient(RemoteConfig{BaseURL: base, Token: testRemoteToken}); err == nil {
			t.Errorf("accepted base %q", base)
		}
	}
	for _, token := range []string{"", "short-token", strings.Repeat("a", 64) + "\n", strings.Repeat("a", 300)} {
		if _, err := NewRemoteClient(RemoteConfig{BaseURL: "http://localhost", Token: token}); err == nil {
			t.Error("accepted invalid token")
		}
	}
	for _, host := range []string{"localhost/path", "user@localhost", "localhost\r\nAuthorization: secret", "localhost#x"} {
		if _, err := NewRemoteClient(RemoteConfig{BaseURL: "http://localhost", Token: testRemoteToken, Host: host}); err == nil {
			t.Errorf("accepted host %q", host)
		}
	}
}

func TestUnavailableClientFailsWithoutTransport(t *testing.T) {
	c := NewUnavailableClient(nil)
	if _, err := c.Status(context.Background()); err == nil {
		t.Fatal("status did not fail closed")
	}
	if _, err := c.TaskEvents(context.Background(), "task", 0); err == nil {
		t.Fatal("stream did not fail closed")
	}
	if err := c.CancelTask(context.Background(), "task"); err == nil {
		t.Fatal("mutation did not fail closed")
	}
}

func TestRemoteRejectsInvalidTrafficToken(t *testing.T) {
	for _, token := range []string{"token\r\nInjected: value", "token\x00", "token with space"} {
		if _, err := NewRemoteClient(RemoteConfig{BaseURL: "http://localhost", Token: testRemoteToken, TrafficAccessToken: token}); err == nil {
			t.Fatal("accepted invalid traffic token")
		}
	}
}
