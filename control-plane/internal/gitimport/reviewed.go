package gitimport

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
)

// ValidateReviewedRepoURL is the Cube host-side fetch boundary. Arbitrary
// tenant URLs must not turn the control plane into an SSRF client. DNS is pinned
// separately at clone time; redirects and environment proxy discovery are off.
func ValidateReviewedRepoURL(raw string) error {
	if e := ValidateRepoURL(raw); e != nil {
		return e
	}
	u, _ := url.Parse(raw)
	if u.Port() != "" && u.Port() != "443" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.RawPath != "" {
		return errors.New("unsupported repository URL")
	}
	switch u.Hostname() {
	case "github.com", "gitlab.com", "bitbucket.org":
	default:
		return errors.New("repository host is not reviewed for Cube import")
	}
	if u.Path == "" || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "\\") {
		return errors.New("invalid repository path")
	}
	return nil
}
func CloneReviewed(ctx context.Context, spec Spec) error {
	if e := ValidateReviewedRepoURL(spec.RepoURL); e != nil {
		return e
	}
	u, _ := url.Parse(spec.RepoURL)
	addresses, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if e != nil {
		return errors.New("repository DNS lookup failed")
	}
	for _, a := range addresses {
		ip := a.IP.To4()
		if ip == nil {
			continue
		}
		if !publicGitIP(ip) {
			return errors.New("repository DNS resolved to a protected address")
		}
		spec.reviewedResolve = u.Hostname() + ":443:" + ip.String()
		break
	}
	if spec.reviewedResolve == "" {
		return errors.New("repository has no approved IPv4 destination")
	}
	return Clone(ctx, spec)
}
func publicGitIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
