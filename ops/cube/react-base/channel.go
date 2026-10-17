// Disposable canary channel: authenticate normally, reject every outbound dial.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func main() {
	if len(os.Args) != 2 {
		panic("expected private journal directory")
	}
	read := func(name string, target any) {
		b, err := os.ReadFile(filepath.Join(os.Args[1], name))
		if err != nil {
			panic(err)
		}
		if json.Unmarshal(b, target) != nil {
			panic("invalid journal")
		}
	}
	var intent struct {
		EnvVars map[string]string `json:"envVars"`
	}
	var created struct {
		ID    string `json:"sandboxID"`
		Token string `json:"trafficAccessToken"`
	}
	read("create-intent.PRIVATE.json", &intent)
	read("created.PRIVATE.json", &created)
	client, err := rt.NewRemoteClient(rt.RemoteConfig{BaseURL: "http://127.0.0.1:80", Host: "3031-" + created.ID + ".cube.app", Token: intent.EnvVars["RUNTIMED_HTTP_TOKEN"], TrafficAccessToken: created.Token})
	if err != nil {
		panic("invalid transport")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	policy := egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}
	if policy.Validate() != nil {
		panic("invalid offline policy")
	}
	conn, err := client.OpenEgressChannel(ctx)
	if err != nil {
		panic("channel unavailable")
	}
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	fmt.Println("CANARY_CHANNEL_READY")
	err = egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: created.ID, Generation: "react-base-canary"}, Policy: policy, DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("offline canary") }})
	if err != nil && ctx.Err() == nil {
		panic("canary channel disconnected")
	}
}
