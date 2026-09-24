// Disposable reviewed-template functional fixture. No tenant or production state.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

type guest struct {
	sb     *cube.Sandbox
	token  string
	client *rt.Client
	cancel context.CancelFunc
}

func require(err error) {
	if err != nil {
		panic(err)
	}
}
func remote(g *guest) *rt.Client {
	c, e := rt.NewRemoteClient(rt.RemoteConfig{BaseURL: "http://127.0.0.1:80", Host: "3031-" + g.sb.SandboxID + ".cube.app", Token: g.token, TrafficAccessToken: g.sb.TrafficAccessToken})
	require(e)
	return c
}
func attach(ctx context.Context, g *guest) {
	var err error
	for i := 0; i < 40; i++ {
		conn, e := g.client.OpenEgressChannel(ctx)
		err = e
		if e == nil {
			channelCtx, cancel := context.WithCancel(ctx)
			g.cancel = cancel
			go func() {
				_ = egress.RunHost(channelCtx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: g.sb.SandboxID, Generation: "reviewed-fixture"}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}, DialContext: func(context.Context, string, string) (net.Conn, error) {
					return nil, errors.New("fixture has no outbound dial")
				}, Services: map[string]http.Handler{"bridge": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"fixture":true}`)
				})}})
			}()
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	require(err)
}
func ready(ctx context.Context, g *guest) *rt.Status {
	for i := 0; i < 200; i++ {
		s, e := g.client.Status(ctx)
		if e == nil && s.Preview.Status == rt.PreviewReady {
			return s
		}
		time.Sleep(200 * time.Millisecond)
	}
	panic("reviewed frontend did not become ready")
}
func page(g *guest, path string) []byte {
	req, e := http.NewRequest("GET", "http://127.0.0.1:80"+path, nil)
	require(e)
	req.Host = "3000-" + g.sb.SandboxID + ".cube.app"
	req.Header.Set("cube-traffic-access-token", g.sb.TrafficAccessToken)
	c := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := c.Do(req)
	require(e)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		panic(fmt.Sprintf("frontend HTTP %d", res.StatusCode))
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	require(e)
	return b
}
func main() {
	if len(os.Args) != 5 || os.Args[1] != "preset" {
		fmt.Fprintln(os.Stderr, "usage: reviewed-pilot preset TEMPLATE PRESET REPORT")
		os.Exit(2)
	}
	if err := runPreset(os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
