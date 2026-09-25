package migration

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestMigrationBrokerScopedServicesFenceAppAndGeneration(t *testing.T) {
	const app = "01M211R5G8WGH2PKNAJ3NGG6V2"
	const address = "10.40.14.68:8080"
	policy := egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}
	raw := fmt.Sprintf(`{%q:[{"origin":"http://%s","routes":{"GET":["/health"]}}]}`, app, address)
	b, err := NewMigrationBroker(context.Background(), policy, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	// Replace only the validated fixed upstream callback with a local fixture.
	// The real guest channel and broker's app/generation selection remain active.
	b.httpServices[app][address] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			entered <- struct{}{}
			<-r.Context().Done()
			cancelled <- struct{}{}
			return
		}
		if r.Header.Get("Authorization") != "Bearer app-owned-key" {
			t.Error("app credential lost")
		}
		io.WriteString(w, "scoped-ok")
	})
	newGuest := func() (*egress.Guest, *runtime.Client, *http.Client) {
		token := strings.Repeat("a", 64)
		g, e := egress.NewGuest(egress.GuestOptions{Authenticate: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+token }})
		if e != nil {
			t.Fatal(e)
		}
		control := httptest.NewServer(g.ChannelHandler())
		proxy := httptest.NewServer(g.ProxyHandler())
		t.Cleanup(control.Close)
		t.Cleanup(proxy.Close)
		t.Cleanup(func() { g.Close() })
		c, e := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: control.URL, Token: token})
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(proxy.URL)
		transport := &http.Transport{Proxy: http.ProxyURL(u)}
		t.Cleanup(transport.CloseIdleConnections)
		return g, c, &http.Client{Transport: transport, Timeout: 2 * time.Second}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g, c, httpClient := newGuest()
	m := &store.RuntimeMigration{SandboxID: "owner", Source: store.Sandbox{AppID: sql.NullString{String: app, Valid: true}}, Binding: store.RuntimeBinding{RuntimeID: "generation-one", TokenCiphertext: []byte("one")}}
	if err = b.Attach(ctx, m, c); err != nil {
		t.Fatal(err)
	}
	if err = g.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://"+address+"/health", nil)
	req.Header.Set("Authorization", "Bearer app-owned-key")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "scoped-ok" {
		t.Fatal("owner service unavailable", resp.StatusCode)
	}
	for _, foreign := range []string{"01M211R5G8WGH2PKNAJ3NGG6V3", "01M211R5G8WGH2PKNAJ3NGG6V4"} {
		other, client, browser := newGuest()
		journal := &store.RuntimeMigration{SandboxID: foreign, Source: store.Sandbox{AppID: sql.NullString{String: foreign, Valid: true}}, Binding: store.RuntimeBinding{RuntimeID: foreign}}
		if err = b.Attach(ctx, journal, client); err != nil {
			t.Fatal(err)
		}
		if err = other.WaitReady(ctx); err != nil {
			t.Fatal(err)
		}
		response, e := browser.Get("http://" + address + "/health")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				t.Fatal("sibling/remix inherited service")
			}
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, e := httpClient.Get("http://" + address + "/wait")
		if e == nil {
			r.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("old request did not enter")
	}
	next, nextClient, _ := newGuest()
	m.Binding.RuntimeID = "generation-two"
	if err = b.Attach(ctx, m, nextClient); err != nil {
		t.Fatal(err)
	}
	if err = next.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("replaced generation retained service request")
	}
	<-done
	// A journal with the same sandbox/binding but a different frozen app identity
	// must also replace the channel and lose the old app's capabilities.
	m.Source.AppID.String = "01M211R5G8WGH2PKNAJ3NGG6V3"
	final, finalClient, finalBrowser := newGuest()
	if err = b.Attach(ctx, m, finalClient); err != nil {
		t.Fatal(err)
	}
	if err = final.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	response, e := finalBrowser.Get("http://" + address + "/health")
	if e == nil {
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("changed journal app retained capability")
		}
	}
}

func TestMigrationBrokerRejectsProtectedScopedService(t *testing.T) {
	p := egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	raw := `{"01M211R5G8WGH2PKNAJ3NGG6V2":[{"origin":"http://10.40.14.68:8080","routes":{"GET":["/health"]}}]}`
	if b, e := NewMigrationBroker(context.Background(), p, raw); e == nil {
		b.Close()
		t.Fatal("offline policy allowed protected infrastructure")
	}
}
