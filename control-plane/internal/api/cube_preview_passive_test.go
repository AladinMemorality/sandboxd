package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/activity"
)

func TestPassiveCubePreviewExactClassification(t *testing.T) {
	for _, tc := range []struct {
		method, upgrade, connection, protocol, accept string
		passive                                       bool
	}{
		{"GET", "websocket", "keep-alive, Upgrade", "vite-hmr", "", true},
		{"GET", "", "", "", "text/x-vite-ping", true},
		{"POST", "websocket", "Upgrade", "vite-hmr", "", false},
		{"GET", "websocket", "Upgrade", "chat", "", false},
		{"GET", "websocket", "Upgrade", "vite-hmr, chat", "", false},
		{"GET", "websocket", "", "vite-hmr", "", false},
		{"GET", "", "", "", "text/x-vite-ping, text/html", false},
		{"GET", "", "", "", "text/x-vite-ping; q=1", false},
	} {
		r := httptest.NewRequest(tc.method, "/", nil)
		r.Header.Set("Upgrade", tc.upgrade)
		r.Header.Set("Connection", tc.connection)
		r.Header.Set("Sec-WebSocket-Protocol", tc.protocol)
		r.Header.Set("Accept", tc.accept)
		if passiveCubePreview(r) != tc.passive {
			t.Fatalf("classification %+v", tc)
		}
	}
}

func TestCubePassivePingRequiresProvenRunningStateWithoutActivity(t *testing.T) {
	for _, state := range []string{"running", "paused", "unknown", "error", "unproven", "locally-stopped", "unauthorized"} {
		t.Run(state, func(t *testing.T) {
			opt := []string{state}
			if state == "unproven" {
				opt = nil
			}
			if state == "locally-stopped" || state == "unauthorized" {
				opt = []string{"running"}
			}
			s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "pong") }, opt...)
			s.Inflight = activity.NewInflightExec()
			old := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			if state != "locally-stopped" {
				if e := s.Store.MarkRunningWoke(context.Background(), cubePreviewTestID, "", "", old); e != nil {
					t.Fatal(e)
				}
			}
			before, _ := s.Store.Get(context.Background(), cubePreviewTestID)
			r := cubePreviewRequest(t, "GET", "/", " ")
			r.Header.Set("Accept", "text/x-vite-ping")
			if state == "unauthorized" {
				r.Header.Del("Cookie")
			}
			w := httptest.NewRecorder()
			s.TryServeCubePreview(w, r)
			if state == "running" {
				if w.Code != 200 || calls.Load() != 1 {
					t.Fatalf("proven ping failed: %d", w.Code)
				}
			} else if w.Code == 200 || calls.Load() != 0 {
				t.Fatal("unproven/paused/authfailed ping contacted guest")
			}
			after, _ := s.Store.Get(context.Background(), cubePreviewTestID)
			if !after.LastActiveAt.Equal(before.LastActiveAt) || connects.Load() != 0 || s.Inflight.Active(cubePreviewTestID) {
				t.Fatal("passive ping renewed or registered activity")
			}
			if _, ok := s.cubePreviewLeases.Load(cubePreviewTestID); ok {
				t.Fatal("passive ping established lease")
			}
		})
	}
}

func TestCubeLiveHMRDoesNotPinWhileApplicationWebSocketDoes(t *testing.T) {
	for _, protocol := range []string{"vite-hmr", "app-live"} {
		t.Run(protocol, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			opt := []string{}
			if protocol == "vite-hmr" {
				opt = []string{"running"}
			}
			s, connects, _ := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
				u := websocket.Upgrader{Subprotocols: []string{protocol}, CheckOrigin: func(*http.Request) bool { return true }}
				c, e := u.Upgrade(w, r, nil)
				if e != nil {
					t.Error(e)
					return
				}
				defer c.Close()
				close(entered)
				<-release
			}, opt...)
			s.Inflight = activity.NewInflightExec()
			old := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			if protocol == "vite-hmr" {
				if e := s.Store.MarkRunningWoke(context.Background(), cubePreviewTestID, "", "", old); e != nil {
					t.Fatal(e)
				}
			}
			front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.TryServeCubePreview(w, r) }))
			defer front.Close()
			r := cubePreviewRequest(t, "GET", "/hmr", "")
			h := r.Header.Clone()
			h.Set("Host", strings.ToLower(cubePreviewTestHost))
			h.Set("Origin", "https://"+strings.ToLower(cubePreviewTestHost))
			dialer := websocket.Dialer{Subprotocols: []string{protocol}, HandshakeTimeout: 5 * time.Second}
			c, _, e := dialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/hmr", h)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			<-entered
			if s.Inflight.Active(cubePreviewTestID) != (protocol == "app-live") {
				t.Fatal("wrong live WebSocket activity classification")
			}
			if protocol == "vite-hmr" {
				after, _ := s.Store.Get(context.Background(), cubePreviewTestID)
				if !after.LastActiveAt.Equal(old) || connects.Load() != 0 {
					t.Fatal("HMR kept app awake")
				}
			}
		})
	}
}
