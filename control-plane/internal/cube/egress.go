package cube

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// ErrUnsafeDomainEgress means the requested policy cannot satisfy mandatory
// host/metadata isolation on the supported unpatched Cube v0.7.1 dataplane.
var ErrUnsafeDomainEgress = errors.New("Cube v0.7.1 domain allow rules override private and management destination denies; domain egress requires a verified worker hard-deny patch")

// OperatorEgressPolicy accepts only operator configuration. Deliberately no
// attestation boolean can enable a policy that has not been tested against the
// patched worker: domain egress stays fail-closed until that integration exists.
// The IPv4 catch-all denies every destination, including public management IPs.
// Native IPv6 is dropped by CubeVS mvmtap, not by its IPv4-only DenyOut trie.
func OperatorEgressPolicy(domainsCSV string) (*NetworkPolicy, error) {
	if len(domainsCSV) > 4096 {
		return nil, errors.New("Cube egress domain configuration is too large")
	}
	if strings.TrimSpace(domainsCSV) == "" {
		return &NetworkPolicy{AllowPublicTraffic: false, AllowOut: []string{}, DenyOut: []string{"0.0.0.0/0"}}, nil
	}
	domains := strings.Split(domainsCSV, ",")
	if len(domains) > 64 {
		return nil, errors.New("too many Cube egress domains")
	}
	label := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	for _, raw := range domains {
		domain := strings.ToLower(strings.TrimSpace(raw))
		parts := strings.Split(domain, ".")
		_, ipErr := netip.ParseAddr(domain)
		if len(domain) > 253 || len(parts) < 2 || ipErr == nil || strings.ContainsAny(domain, "*/:@?#\\") {
			return nil, fmt.Errorf("Cube egress requires exact DNS names, not URLs, IPs, CIDRs or wildcards")
		}
		for _, part := range parts {
			if len(part) > 63 || !label.MatchString(part) {
				return nil, errors.New("invalid Cube egress domain")
			}
		}
	}
	return nil, ErrUnsafeDomainEgress
}
