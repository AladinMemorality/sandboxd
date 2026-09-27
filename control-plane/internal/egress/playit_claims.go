package egress

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net/netip"
	"sync"
	"time"
)

// This is the admission primitive for the proposed Playit transport. It does
// not register a service, open a socket, or change public egress policy.
const (
	playitClaimTTL   = 15 * time.Second
	playitMaxPending = 128
	playitMaxSeen    = 4096
	playitMaxToken   = 1024
)

type playitClaim struct {
	tunnel netip.AddrPort
	relay  netip.AddrPort
	ticket [32]byte
}

// The ticket is a capability. Formatting a claim must not disclose it.
func (playitClaim) String() string     { return "Playit claim (redacted)" }
func (c playitClaim) GoString() string { return c.String() }

// Playit v0.15.26 ControlFeed::NewClient, using message-encoding 0.2.2.
// Bound the wire input before reading its u64 length; never allocate from it.
// The peer address may be IPv6, but host destinations remain IPv4-only.
func parsePlayitClaim(packet []byte) (playitClaim, error) {
	var zero playitClaim
	if len(packet) < 4 || len(packet) > 4+3*19+8+playitMaxToken+12 || binary.BigEndian.Uint32(packet[:4]) != 2 {
		return zero, ErrDenied
	}
	b := packet[4:]
	address := func() (netip.AddrPort, bool) {
		if len(b) < 1 {
			return netip.AddrPort{}, false
		}
		n := 0
		switch b[0] {
		case 4:
			n = 4
		case 6:
			n = 16
		default:
			return netip.AddrPort{}, false
		}
		if len(b) < 1+n+2 {
			return netip.AddrPort{}, false
		}
		ip, ok := netip.AddrFromSlice(b[1 : 1+n])
		port := binary.BigEndian.Uint16(b[1+n : 1+n+2])
		b = b[1+n+2:]
		return netip.AddrPortFrom(ip, port), ok && port != 0
	}
	tunnel, ok := address()
	if !ok {
		return zero, ErrDenied
	}
	if _, ok = address(); !ok {
		return zero, ErrDenied
	}
	relay, ok := address()
	if !ok || len(b) < 8 {
		return zero, ErrDenied
	}
	n := binary.BigEndian.Uint64(b[:8])
	b = b[8:]
	if n == 0 || n > playitMaxToken || uint64(len(b)) != n+12 {
		return zero, ErrDenied
	}
	return playitClaim{tunnel: tunnel, relay: relay, ticket: sha256.Sum256(b[:int(n)])}, nil
}

type playitPermit struct {
	relay   netip.AddrPort
	expires time.Time
}

// One instance belongs to one authenticated channel and exact persisted
// runtime generation. Only a trusted UDP receive loop may call observe; never
// accept an alleged provider packet or source address from a guest request.
// The eventual dialer must separately reauthorize the current app binding and
// cancel every connection on channel revocation. This ledger performs no I/O.
type playitClaims struct {
	mu       sync.Mutex
	ctx      context.Context
	identity Identity
	policy   Policy
	sources  map[netip.AddrPort]bool
	tunnels  map[netip.AddrPort]bool
	pending  map[[32]byte]playitPermit
	seen     map[[32]byte]bool
	now      func() time.Time
}

func newPlayitClaims(ctx context.Context, identity Identity, policy Policy, sources, tunnels []netip.AddrPort) (*playitClaims, error) {
	if ctx == nil || ctx.Err() != nil || identity.SandboxID == "" || identity.Generation == "" || policy.Validate() != nil || len(sources) == 0 || len(sources) > 16 || len(tunnels) == 0 || len(tunnels) > 16 {
		return nil, ErrDenied
	}
	c := &playitClaims{ctx: ctx, identity: identity, policy: policy, sources: make(map[netip.AddrPort]bool), tunnels: make(map[netip.AddrPort]bool), pending: make(map[[32]byte]playitPermit), seen: make(map[[32]byte]bool), now: time.Now}
	c.policy.ProtectedPrefixes = append([]netip.Prefix(nil), policy.ProtectedPrefixes...)
	for _, source := range sources {
		if !c.policy.public(source.Addr()) || source.Port() != 5525 || c.sources[source] {
			return nil, ErrDenied
		}
		c.sources[source] = true
	}
	for _, tunnel := range tunnels {
		if !c.policy.public(tunnel.Addr()) || tunnel.Port() == 0 || c.tunnels[tunnel] {
			return nil, ErrDenied
		}
		c.tunnels[tunnel] = true
	}
	return c, nil
}

func (c *playitClaims) expire(now time.Time) {
	for ticket, permit := range c.pending {
		if !now.Before(permit.expires) {
			delete(c.pending, ticket)
		}
	}
}

func (c *playitClaims) observe(source netip.AddrPort, packet []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil || !c.sources[source] {
		return ErrDenied
	}
	claim, err := parsePlayitClaim(packet)
	if err != nil || !c.tunnels[claim.tunnel] || !c.policy.public(claim.relay.Addr()) {
		return ErrDenied
	}
	now := c.now()
	c.expire(now)
	// Retain bounded tombstones for the channel lifetime. A retransmission must
	// neither extend the deadline nor resurrect a consumed or expired ticket.
	if c.seen[claim.ticket] {
		return ErrDenied
	}
	if len(c.pending) >= playitMaxPending || len(c.seen) >= playitMaxSeen {
		return ErrDenied
	}
	c.seen[claim.ticket] = true
	c.pending[claim.ticket] = playitPermit{relay: claim.relay, expires: now.Add(playitClaimTTL)}
	return nil
}

// Take consumes before dialing, including when the subsequent dial fails.
// No guest-selected relay address or port is accepted.
func (c *playitClaims) take(identity Identity, ticket [32]byte) (netip.AddrPort, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil || identity != c.identity {
		return netip.AddrPort{}, ErrDenied
	}
	c.expire(c.now())
	permit, ok := c.pending[ticket]
	if !ok {
		return netip.AddrPort{}, ErrDenied
	}
	delete(c.pending, ticket)
	return permit.relay, nil
}
