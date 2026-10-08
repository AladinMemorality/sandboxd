package egress

import (
	"context"
	"net/netip"
	"testing"
)

func TestRegistryOnlyRecovery(t *testing.T) {
	p := testPolicy()
	p.AllowedHosts = []string{"registry.npmjs.org"}
	p.Ports = []uint16{443}
	for _, host := range []string{"registry.npmjs.org.evil.test", "a.registry.npmjs.org", "api.openai.com", "api.anthropic.com", "93.184.216.34", "127.0.0.1"} {
		if _, err := p.Destination(context.Background(), host, 443); err == nil {
			t.Fatalf("allowed unlisted host %s", host)
		}
	}
	if _, err := p.Destination(context.Background(), "REGISTRY.NPMJS.ORG.", 443); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Destination(context.Background(), "registry.npmjs.org", 80); err == nil {
		t.Fatal("allowed plaintext port")
	}
	p.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
	})
	if _, err := p.Destination(context.Background(), "registry.npmjs.org", 443); err == nil {
		t.Fatal("allowlist bypassed private address protection")
	}
	p = testPolicy()
	p.AllowedHosts = []string{"registry.npmjs.org"}
	p.ProtectedDomains = append(p.ProtectedDomains, "npmjs.org")
	if _, err := p.Destination(context.Background(), "registry.npmjs.org", 443); err == nil {
		t.Fatal("allowlist bypassed protected domain")
	}
	for _, host := range []string{"", "*.npmjs.org", "registry.npmjs.org.", "REGISTRY.NPMJS.ORG", "93.184.216.34"} {
		p.AllowedHosts = []string{host}
		if err := p.Validate(); err == nil {
			t.Fatalf("accepted invalid configuration %q", host)
		}
	}
}
