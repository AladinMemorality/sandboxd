package egress

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// OperatorPolicy is shared by online and offline control paths. Inputs come
// from explicit operator configuration, never from guest frames or app env.
func OperatorPolicy(cidrs, domains string, origins ...string) (Policy, error) {
	p := Policy{}
	if len(cidrs) > 16384 || strings.TrimSpace(cidrs) == "" {
		return p, fmt.Errorf("explicit bounded protected management CIDRs required")
	}
	for _, s := range strings.Split(cidrs, ",") {
		v, e := netip.ParsePrefix(strings.TrimSpace(s))
		if e != nil || !v.Addr().Is4() {
			return p, fmt.Errorf("protected CIDRs must be IPv4 prefixes")
		}
		p.ProtectedPrefixes = append(p.ProtectedPrefixes, v.Masked())
	}
	if len(domains) > 32768 || strings.TrimSpace(domains) == "" {
		return p, fmt.Errorf("explicit bounded protected management domains required")
	}
	for _, s := range strings.Split(domains, ",") {
		p.ProtectedDomains = append(p.ProtectedDomains, strings.TrimSpace(s))
	}
	for _, s := range origins {
		u, e := url.Parse(s)
		if e != nil || u.Hostname() == "" {
			return p, fmt.Errorf("invalid protected service origin")
		}
		host := u.Hostname()
		if ip, e := netip.ParseAddr(host); e == nil {
			if ip.Is4() {
				p.ProtectedPrefixes = append(p.ProtectedPrefixes, netip.PrefixFrom(ip, 32))
			}
		} else {
			p.ProtectedDomains = append(p.ProtectedDomains, host)
		}
	}
	addresses, e := net.InterfaceAddrs()
	if e != nil {
		return p, fmt.Errorf("cannot inventory local addresses")
	}
	for _, address := range addresses {
		if prefix, e := netip.ParsePrefix(address.String()); e == nil && prefix.Addr().Is4() {
			p.ProtectedPrefixes = append(p.ProtectedPrefixes, netip.PrefixFrom(prefix.Addr(), 32))
		}
	}
	return p, p.Validate()
}
