package main

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGatewayPreservesRequestsAndDoesNotCacheAuthorization(t *testing.T) {
	var denied atomic.Bool
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) || r.Header.Get("X-Preview-Worker") != "vps" {
			t.Error("gateway authentication missing")
		}
		if denied.Load() {
			w.WriteHeader(403)
			return
		}
		var in request
		json.NewDecoder(r.Body).Decode(&in)
		if in.Method != "POST" || in.URI != "/save?q=1" || in.Headers.Get("Cookie") == "" {
			t.Error("authorization metadata lost")
		}
		json.NewEncoder(w).Encode(route{Host: "3000-runtime.cube.test", Token: "traffic-secret", Private: true})
	}))
	defer auth.Close()
	calls := 0
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if r.Host != "3000-runtime.cube.test" || string(b) != "original body" || r.Method != "POST" || r.URL.RequestURI() != "/save?q=1" {
			t.Error("app request changed")
		}
		if r.Header.Get("Cube-Traffic-Access-Token") != "traffic-secret" || strings.Contains(r.Header.Get("Cookie"), "sandbox_preview") || strings.Contains(r.Header.Get("Cookie"), "sbx_session") || !strings.Contains(r.Header.Get("Cookie"), "app_session=ok") {
			t.Error("credential boundary failed")
		}
		if r.Header.Get("Authorization") != "Bearer application-token" {
			t.Error("application authorization removed")
		}
		w.Header().Set("Cube-Traffic-Access-Token", "secret")
		w.Header().Set("Authorization", "secret")
		w.Header().Add("Set-Cookie", "app_session=new; Domain=.baarcha.tn; Path=/")
		w.Write([]byte("saved"))
	}))
	defer origin.Close()
	g, e := newGateway(config{Controller: auth.URL, Origin: origin.URL, Worker: "vps", Key: strings.Repeat("k", 32), Domain: "baarcha.tn"})
	if e != nil {
		t.Fatal(e)
	}
	invoke := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/save?q=1", strings.NewReader("original body"))
		r.Host = "s-abc-3000.baarcha.tn"
		r.Header.Set("Cookie", "sandbox_preview=signed; sbx_session=platform; app_session=ok")
		r.Header.Set("Authorization", "Bearer application-token")
		r.Header.Set("Cube-Traffic-Access-Token", "forged")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	w := invoke()
	if w.Code != 200 || w.Body.String() != "saved" || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Cube-Traffic-Access-Token") != "" || w.Header().Get("Authorization") != "" || strings.Contains(w.Header().Get("Set-Cookie"), "Domain") {
		t.Fatal("response boundary failed")
	}
	denied.Store(true)
	w = invoke()
	if w.Code != 403 || calls != 1 {
		t.Fatal("permission revocation did not take effect")
	}
}

func TestGatewayWebSocket(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(route{Host: "3000-runtime.cube.test", Token: "traffic-secret", Passive: true})
	}))
	defer auth.Close()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		kind, b, e := c.ReadMessage()
		if e == nil {
			c.WriteMessage(kind, b)
		}
	}))
	defer origin.Close()
	g, e := newGateway(config{Controller: auth.URL, Origin: origin.URL, Worker: "vps", Key: strings.Repeat("k", 32), Domain: "baarcha.tn"})
	if e != nil {
		t.Fatal(e)
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Host = "s-abc-3000.baarcha.tn"; g.ServeHTTP(w, r) }))
	defer front.Close()
	c, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.WriteMessage(websocket.TextMessage, []byte("live-edit")); e != nil {
		t.Fatal(e)
	}
	_, b, e := c.ReadMessage()
	if e != nil || string(b) != "live-edit" {
		t.Fatal("websocket echo failed", e)
	}
}
