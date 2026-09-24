package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

func TestRemoteEgressChannelUsesScopedPrivateIngress(t *testing.T) {
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/egress/channel" || r.Host != "3031-guest.cube.test" ||
			r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("cube-traffic-access-token") != "guest-ingress" {
			t.Error("channel did not preserve scoped routing and authentication")
			http.Error(w, "unauthorized", 401)
			return
		}
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("ready"))
	}))
	defer server.Close()
	client, err := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Host: "3031-guest.cube.test", Token: token, TrafficAccessToken: "guest-ingress"})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.OpenEgressChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, body, err := conn.ReadMessage()
	if err != nil || string(body) != "ready" {
		t.Fatalf("channel unreadable: %v", err)
	}
}

func TestRemoteEgressChannelNeverFollowsRedirects(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer server.Close()
	client, err := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := client.OpenEgressChannel(context.Background()); err == nil {
		conn.Close()
		t.Fatal("redirect accepted")
	}
	if leaked.Load() {
		t.Fatal("redirect destination received a credential-bearing request")
	}
	if _, err := NewClient("/does/not/exist").OpenEgressChannel(context.Background()); err == nil {
		t.Fatal("Docker client accepted remote channel")
	}
}
