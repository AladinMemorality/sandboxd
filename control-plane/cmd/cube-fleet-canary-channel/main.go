// Operator-only deny-all reverse channel for the isolated native B200 canary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	guest "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func read(path string, value any) error {
	s, err := os.Lstat(path)
	if err != nil || !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || s.Size() > 1<<20 {
		return fmt.Errorf("private fixture input required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func run() error {
	if os.Geteuid() != 0 || len(os.Args) != 2 || filepath.Dir(os.Args[1]) != "/opt/baarcha-bench/cube-fleet-20260927" || !strings.HasPrefix(filepath.Base(os.Args[1]), "native-canary-") {
		return fmt.Errorf("fixed root fixture path required")
	}
	var vm cube.Sandbox
	var intent cube.CreateRequest
	var old guest.Status
	for name, value := range map[string]any{"create-response.PRIVATE.json": &vm, "intent.PRIVATE.json": &intent, "supervisor-status.PRIVATE.json": &old} {
		if err := read(filepath.Join(os.Args[1], name), value); err != nil {
			return err
		}
	}
	if intent.Metadata["fixture"] != "b200-native-canary" || !strings.HasPrefix(intent.Metadata["sandboxd_id"], "fleet-canary-") || len(intent.DistributionScope) != 1 || intent.DistributionScope[0] != "10.254.240.2" {
		return fmt.Errorf("fixture identity mismatch")
	}
	c, err := guest.NewRemoteClient(guest.RemoteConfig{BaseURL: "http://10.254.240.2:28080", Host: "3031-" + vm.SandboxID + ".cube.app", Token: intent.EnvVars["RUNTIMED_HTTP_TOKEN"], TrafficAccessToken: vm.TrafficAccessToken})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	deadline := time.Now().Add(30 * time.Second)
	for {
		s, e := c.Status(ctx)
		if e == nil && !s.Runtimed.BootedAt.IsZero() && !s.Runtimed.BootedAt.Equal(old.Runtimed.BootedAt) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("imported supervisor did not restart")
		}
		time.Sleep(200 * time.Millisecond)
	}
	conn, err := c.OpenEgressChannel(ctx)
	if err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() {
		result <- egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: intent.Metadata["sandboxd_id"], Generation: vm.SandboxID}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}})
	}()
	if err = c.ResumeWorkspace(ctx); err != nil {
		return err
	}
	fmt.Println("CANARY_CHANNEL_READY")
	closed := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(closed) }()
	select {
	case <-closed:
		return nil
	case err = <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Canary channel unavailable")
		os.Exit(1)
	}
}
