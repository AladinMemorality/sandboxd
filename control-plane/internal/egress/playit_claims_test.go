package egress

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Public upstream fixture, not a customer packet or credential:
// playit-cloud/playit-agent e6a7b4e10e6214edf5b00349a107e18b5b8d10f5,
// packages/agent_proto/src/control_feed.rs::test::parse_control.
const playitFixture = "0000000204d1198d10046804d053c766cc4904d1198c029306000000000000004c2c003cd1198d100468d053c766cc49cba8329c930664e9431200000000000000010000000000298c05779c9306000000000e00000000000065b2000171012de0fdb1b6d5de58be82911b07bc00000000000065b20000000e"

func playitPacket(t testing.TB) []byte {
	t.Helper()
	b, err := hex.DecodeString(playitFixture)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func playitLedger(t *testing.T) (*playitClaims, context.CancelFunc, netip.AddrPort, []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	source := netip.MustParseAddrPort("209.25.140.2:5525")
	b := playitPacket(t)
	claim, err := parsePlayitClaim(b)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newPlayitClaims(ctx, Identity{"sandbox-a", "generation-1"}, testPolicy(), []netip.AddrPort{source}, []netip.AddrPort{claim.tunnel})
	if err != nil {
		t.Fatal(err)
	}
	return c, cancel, source, b
}

func TestPlayitUpstreamWireFixture(t *testing.T) {
	c, err := parsePlayitClaim(playitPacket(t))
	if err != nil || c.tunnel.String() != "209.25.141.16:1128" || c.relay.String() != "209.25.140.2:37638" {
		t.Fatal("upstream fixture mismatch", err)
	}
	if got := fmt.Sprintf("%v %+v %#v", c, c, c); strings.Contains(got, hex.EncodeToString(c.ticket[:])) || strings.Contains(got, "ticket:") {
		t.Fatal("ticket leaked through formatting")
	}
}

func TestPlayitMalformedPackets(t *testing.T) {
	b := playitPacket(t)
	for i := 0; i < len(b); i++ {
		if _, err := parsePlayitClaim(b[:i]); err == nil {
			t.Fatalf("accepted truncated packet %d", i)
		}
	}
	for _, offset := range []int{0, 4, 11, 18} {
		bad := append([]byte(nil), b...)
		bad[offset] = 0xff
		if _, err := parsePlayitClaim(bad); err == nil {
			t.Fatalf("accepted invalid tag at %d", offset)
		}
	}
	for _, n := range []uint64{0, 1, 75, 77, 1025, ^uint64(0)} {
		bad := append([]byte(nil), b...)
		binary.BigEndian.PutUint64(bad[25:33], n)
		if _, err := parsePlayitClaim(bad); err == nil {
			t.Fatal("accepted invalid token length", n)
		}
	}
	if _, err := parsePlayitClaim(append(b, 0)); err == nil {
		t.Fatal("accepted trailing data")
	}
}

func TestPlayitTicketScopeReplayAndRevocation(t *testing.T) {
	c, cancel, source, b := playitLedger(t)
	claim, _ := parsePlayitClaim(b)
	if c.observe(source, b) != nil {
		t.Fatal("fixture rejected")
	}
	for _, identity := range []Identity{{"sandbox-b", "generation-1"}, {"sandbox-a", "generation-2"}} {
		if _, err := c.take(identity, claim.ticket); err == nil {
			t.Fatal("foreign identity accepted")
		}
	}
	if relay, err := c.take(c.identity, claim.ticket); err != nil || relay != claim.relay {
		t.Fatal("owner ticket failed")
	}
	if _, err := c.take(c.identity, claim.ticket); err == nil {
		t.Fatal("replay accepted")
	}
	if c.observe(source, b) == nil {
		t.Fatal("consumed ticket resurrected")
	}
	b[33] ^= 1
	if c.observe(source, b) != nil {
		t.Fatal("second fixture rejected")
	}
	claim, _ = parsePlayitClaim(b)
	cancel()
	if _, err := c.take(c.identity, claim.ticket); err == nil {
		t.Fatal("revoked channel accepted")
	}
	if c.observe(source, b) == nil {
		t.Fatal("revoked channel admitted packet")
	}
}

func TestPlayitDeadlineNotExtendedByRetransmission(t *testing.T) {
	c, _, source, b := playitLedger(t)
	claim, _ := parsePlayitClaim(b)
	now := time.Unix(1, 0)
	c.now = func() time.Time { return now }
	if c.observe(source, b) != nil {
		t.Fatal("fixture rejected")
	}
	now = now.Add(14 * time.Second)
	if c.observe(source, b) == nil {
		t.Fatal("duplicate accepted")
	}
	now = now.Add(time.Second)
	if _, err := c.take(c.identity, claim.ticket); err == nil {
		t.Fatal("expired ticket accepted")
	}
	if c.observe(source, b) == nil {
		t.Fatal("expired ticket resurrected")
	}
}

func TestPlayitUnapprovedSourceTunnelAndPrivateRelay(t *testing.T) {
	c, _, source, b := playitLedger(t)
	for _, other := range []string{"209.25.140.3:5525", "209.25.140.2:5526", "127.0.0.1:5525"} {
		if c.observe(netip.MustParseAddrPort(other), b) == nil {
			t.Fatal("unapproved source accepted")
		}
	}
	bad := append([]byte(nil), b...)
	bad[10] ^= 1
	if c.observe(source, bad) == nil {
		t.Fatal("other tunnel accepted")
	}
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "65.108.225.153", "0.0.0.0", "224.0.0.1"} {
		bad = append([]byte(nil), b...)
		raw := netip.MustParseAddr(ip).As4()
		copy(bad[19:23], raw[:])
		if c.observe(source, bad) == nil {
			t.Fatal("private/protected relay accepted", ip)
		}
	}
	if len(c.pending) != 0 || len(c.seen) != 0 {
		t.Fatal("rejected packets consumed admission capacity")
	}
}

func TestPlayitAdmissionBoundsAndAtomicConsumption(t *testing.T) {
	c, _, source, b := playitLedger(t)
	for i := 0; i < playitMaxPending; i++ {
		binary.BigEndian.PutUint32(b[33:37], uint32(i))
		if c.observe(source, b) != nil {
			t.Fatal("early capacity rejection")
		}
	}
	binary.BigEndian.PutUint32(b[33:37], playitMaxPending)
	if c.observe(source, b) == nil {
		t.Fatal("pending limit exceeded")
	}
	binary.BigEndian.PutUint32(b[33:37], 0)
	claim, _ := parsePlayitClaim(b)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.take(c.identity, claim.ticket); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("ticket consumed more than once")
	}
	for i := playitMaxPending; i < playitMaxSeen; i++ {
		binary.BigEndian.PutUint32(b[33:37], uint32(i))
		claim, _ = parsePlayitClaim(b)
		if c.observe(source, b) != nil {
			t.Fatal("unexpected tombstone limit", i)
		}
		if _, err := c.take(c.identity, claim.ticket); err != nil {
			t.Fatal(err)
		}
	}
	binary.BigEndian.PutUint32(b[33:37], playitMaxSeen)
	if c.observe(source, b) == nil {
		t.Fatal("seen limit exceeded")
	}
}

func TestPlayitConfigurationRefusesInvalidSources(t *testing.T) {
	tunnel := netip.MustParseAddrPort("209.25.141.16:1128")
	for _, sources := range [][]netip.AddrPort{nil, {netip.MustParseAddrPort("127.0.0.1:5525")}, {netip.MustParseAddrPort("209.25.140.2:80")}, {tunnel, tunnel}} {
		if _, err := newPlayitClaims(context.Background(), Identity{"a", "1"}, testPolicy(), sources, []netip.AddrPort{tunnel}); err == nil {
			t.Fatal("invalid source configuration accepted")
		}
	}
}

func FuzzPlayitClaimParser(f *testing.F) {
	f.Add(playitPacket(f))
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 2})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := parsePlayitClaim(b)
		if err == nil && (!c.tunnel.IsValid() || !c.relay.IsValid() || c.tunnel.Port() == 0 || c.relay.Port() == 0) {
			t.Fatal("invalid successful parse")
		}
	})
}
