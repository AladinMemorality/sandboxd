package migration

import (
	"context"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMigrationBrokerAuthenticatedAttachReplacementAndClose(t *testing.T) {
	b, e := NewMigrationBroker(context.Background(), egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	var active atomic.Int32
	server := func(token string) (*egress.Guest, *runtime.Client) {
		token = strings.Repeat(token, 64)
		g, e := egress.NewGuest(egress.GuestOptions{Authenticate: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+token }})
		if e != nil {
			t.Fatal(e)
		}
		h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			active.Add(1)
			defer active.Add(-1)
			g.ChannelHandler().ServeHTTP(w, r)
		}))
		t.Cleanup(h.Close)
		t.Cleanup(func() { _ = g.Close() })
		c, e := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: h.URL, Token: token})
		if e != nil {
			t.Fatal(e)
		}
		return g, c
	}
	g, c := server("1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := &store.RuntimeMigration{SandboxID: "stable-one", Binding: store.RuntimeBinding{RuntimeID: "vm-one", TokenCiphertext: []byte("one"), TokenNonce: []byte("nonce")}}
	if e = b.Attach(ctx, m, c); e != nil {
		t.Fatal(e)
	}
	if e = g.WaitReady(ctx); e != nil {
		t.Fatal(e)
	}
	g2, c2 := server("2")
	m.Binding.TokenCiphertext = []byte("two")
	if e = b.Attach(ctx, m, c2); e != nil {
		t.Fatal(e)
	}
	if e = g2.WaitReady(ctx); e != nil {
		t.Fatal(e)
	}
	b.Detach(m.SandboxID)
	if e = b.Attach(ctx, m, c2); e != nil {
		t.Fatal(e)
	}
	b.Close()
	b.Close()
	if e = b.Attach(ctx, m, c2); e == nil {
		t.Fatal("closed broker admitted attach")
	}
	deadline := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatal("authenticated channels leaked")
	}
}
func TestMigrationBrokerFailedAuthenticationAndInvalidPolicy(t *testing.T) {
	if b, e := NewMigrationBroker(context.Background(), egress.Policy{}); e == nil {
		b.Close()
		t.Fatal("missing protected addresses accepted")
	}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer h.Close()
	c, e := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: h.URL, Token: strings.Repeat("a", 64)})
	if e != nil {
		t.Fatal(e)
	}
	b, e := NewMigrationBroker(context.Background(), egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if e = b.Attach(ctx, &store.RuntimeMigration{SandboxID: "one", Binding: store.RuntimeBinding{RuntimeID: "vm"}}, c); e == nil {
		t.Fatal("unauthorized channel marked ready")
	}
	b.Close()
}

func TestMigrationBrokerReattachesAfterSupervisorRestartAndParentCancel(t *testing.T) {
	var current atomic.Pointer[egress.Guest]
	fresh := func() *egress.Guest {
		g, e := egress.NewGuest(egress.GuestOptions{Authenticate: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+strings.Repeat("1", 64) }})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = g.Close() })
		return g
	}
	first := fresh()
	current.Store(first)
	var count atomic.Int32
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		current.Load().ChannelHandler().ServeHTTP(w, r)
	}))
	defer h.Close()
	c, e := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: h.URL, Token: strings.Repeat("1", 64)})
	if e != nil {
		t.Fatal(e)
	}
	life, stop := context.WithCancel(context.Background())
	defer stop()
	b, e := NewMigrationBroker(life, egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := &store.RuntimeMigration{SandboxID: "one", Binding: store.RuntimeBinding{RuntimeID: "vm", Domain: "old.example.com", TemplateID: "old"}}
	if e = b.Attach(ctx, m, c); e != nil {
		t.Fatal(e)
	}
	if e = first.WaitReady(ctx); e != nil {
		t.Fatal(e)
	}
	second := fresh()
	current.Store(second)
	first.Close()
	if e = second.WaitReady(ctx); e != nil {
		t.Fatal("supervisor channel was not reattached", e)
	}
	for _, mutate := range []func(){func() { m.Binding.Domain = "new.example.com" }, func() { m.Binding.TemplateID = "new" }} {
		before := count.Load()
		mutate()
		if e = b.Attach(ctx, m, c); e != nil {
			t.Fatal(e)
		}
		if count.Load() <= before {
			t.Fatal("binding route/template change reused old channel")
		}
	}
	stop()
	if e = b.Attach(ctx, m, c); e == nil {
		t.Fatal("cancelled parent admitted stale connected state")
	}
}
