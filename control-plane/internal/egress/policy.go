// Package egress carries guest requests over a host-initiated authenticated
// channel. It does not open the guest NIC or configure a worker firewall.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

var ErrDenied = errors.New("egress destination is not permitted")

// Resolver is supplied by trusted host code, never by a channel message.
type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// Policy protects first-hop destinations. Public services can themselves be
// application proxies: this is network isolation, not data-loss prevention.
type Policy struct {
	ProtectedPrefixes []netip.Prefix
	ProtectedDomains  []string // exact names and their subdomains
	Ports             []uint16 // empty means TCP 80 and 443
	Resolver          Resolver
}

var reserved = prefixes("0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4")

func prefixes(values ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, value := range values {
		out = append(out, netip.MustParsePrefix(value))
	}
	return out
}

func canonicalHost(raw string) (string, error) {
	if raw == "" || len(raw) > 253 || strings.ContainsAny(raw, "@:/?#%\\\x00\r\n\t ") {
		return "", ErrDenied
	}
	host := strings.ToLower(strings.TrimSuffix(raw, "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		if !ip.Is4() {
			return "", ErrDenied
		}
		return ip.String(), nil
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return "", ErrDenied
	}
	for _, label := range parts {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrDenied
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", ErrDenied
			}
		}
	}
	return host, nil
}

// Validate checks that an explicit bounded management-address inventory exists.
// IPv4 is the only supported dial family; IPv6 protected entries are rejected.
func (p Policy) Validate() error {
	if len(p.ProtectedPrefixes) == 0 || len(p.ProtectedPrefixes) > 128 || len(p.ProtectedDomains) > 128 || len(p.Ports) > 32 {
		return errors.New("explicit bounded management-address inventory required")
	}
	for _, prefix := range p.ProtectedPrefixes {
		if !prefix.IsValid() || !prefix.Addr().Is4() {
			return errors.New("invalid protected prefix")
		}
	}
	for _, domain := range p.ProtectedDomains {
		if _, err := canonicalHost(domain); err != nil {
			return errors.New("invalid protected domain")
		}
	}
	for _, port := range p.Ports {
		if port == 0 {
			return errors.New("invalid allowed port")
		}
	}
	return nil
}

func (p Policy) public(ip netip.Addr) bool {
	// IPv6 and mapped literals are deliberately unsupported in this version.
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range reserved {
		if prefix.Contains(ip) {
			return false
		}
	}
	for _, prefix := range p.ProtectedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// Destination resolves IPv4 A records once, rejects the entire A answer set if any answer is
// protected, and returns a numeric dial address. Dial must not resolve again.
func (p Policy) Destination(ctx context.Context, raw string, port uint16) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	host, err := canonicalHost(raw)
	if err != nil {
		return "", err
	}
	for _, domain := range p.ProtectedDomains {
		domain, _ = canonicalHost(domain)
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return "", ErrDenied
		}
	}
	allowed := port == 80 || port == 443
	if len(p.Ports) != 0 {
		allowed = false
		for _, candidate := range p.Ports {
			if port == candidate {
				allowed = true
			}
		}
	}
	if !allowed {
		return "", ErrDenied
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		resolver := p.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		addresses, err = resolver.LookupNetIP(ctx, "ip4", host)
		if err != nil {
			return "", fmt.Errorf("resolve destination: %w", err)
		}
	}
	if len(addresses) == 0 || len(addresses) > 64 {
		return "", ErrDenied
	}
	for _, address := range addresses {
		if !p.public(address) {
			return "", ErrDenied
		}
	}
	return net.JoinHostPort(addresses[0].String(), strconv.Itoa(int(port))), nil
}
