//go:build linux

// This opt-in program has NO attach, pin, existing-map lookup, socket or network
// transmission code. Default mode parses ELF metadata only. Review before use.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"
)

const guestA uint32 = 42420
const guestB uint32 = 42421
const ttl uint64 = 3600 * 1000000000

var expected = map[string]string{
	"mvmtap_x86_bpfel.o":  "de3e81d3ac7cdb66b65f950013025c85ae9ceb5f7a6ad7d1afe871d30df438fd",
	"nodenic_x86_bpfel.o": "a8bb99b63e7ed322e166e7ddb20d991011238c62a0a7760c3cb1592dbaf01175",
	"localgw_x86_bpfel.o": "d8703e8d3fd386c65edab49608aed78f2646865d32b30a2bda81b69ba74aa38a",
}
var sharedNames = []string{"ifindex_to_mvmmeta", "mvmip_to_ifindex", "baarcha_replies", "local_port_mapping", "remote_port_mapping", "egress_sessions", "ingress_sessions", "allow_out_v3", "deny_out"}
var resetNames = []string{"ifindex_to_mvmmeta", "mvmip_to_ifindex", "baarcha_replies", "local_port_mapping", "remote_port_mapping", "egress_sessions", "ingress_sessions", "allow_out_v3", "deny_out"}
var constants = map[string]interface{}{"nodenic_ifindex": uint32(1), "nodenic_ip": ip("198.18.0.1"), "cubegw0_ifindex": uint32(42422), "cubegw0_ip": ip("198.18.0.2")}

type result struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Runs   int    `json:"program_test_runs"`
	Detail string `json:"detail,omitempty"`
}
type environment struct {
	coll     []*ebpf.Collection
	maps     map[string]*ebpf.Map
	specs    map[string]*ebpf.CollectionSpec
	programs map[string]*ebpf.Program
	owned    []*ebpf.Map
	calls    int
	started  time.Time
}

func main() {
	if err := runMain(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Checked failures unwind all owned FDs before returning to main.
func runMain() (err error) {
	defer func() {
		if value := recover(); value != nil {
			if checked, ok := value.(fixtureFailure); ok {
				err = checked.error
			} else {
				panic(value)
			}
		}
	}()
	dir := flag.String("objects", "", "directory containing reviewed ELF objects")
	helperSHA := flag.String("classifier-sha256", "", "reviewed test-only classifier ELF digest")
	execute := flag.Bool("execute-reviewed-anonymous", false, "requires separate explicit review; creates anonymous maps only")
	flag.Parse()
	if *dir == "" || len(*helperSHA) != 64 {
		fatal(errors.New("explicit object directory and reviewed classifier digest required"))
	}
	specs := map[string]*ebpf.CollectionSpec{}
	names := []string{"mvmtap_x86_bpfel.o", "nodenic_x86_bpfel.o", "localgw_x86_bpfel.o", "classifier_fixture.o"}
	inventory := map[string]interface{}{}
	for _, name := range names {
		path := filepath.Join(*dir, name)
		info, err := os.Lstat(path)
		must(err)
		if !info.Mode().IsRegular() || info.Size() > 32<<20 {
			fatal(errors.New("ELF must be a bounded regular file"))
		}
		data, err := os.ReadFile(path)
		must(err)
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		want := expected[name]
		if name == "classifier_fixture.o" {
			want = *helperSHA
		}
		if digest != want {
			fatal(fmt.Errorf("object digest mismatch: %s", name))
		}
		spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(data))
		must(err)
		meta := map[string]interface{}{}
		for key, m := range spec.Maps {
			meta[key] = map[string]interface{}{"type": m.Type.String(), "key_size": m.KeySize, "value_size": m.ValueSize, "original_max_entries": m.MaxEntries}
		}
		for _, p := range spec.Programs {
			if p.Type != ebpf.SchedCLS {
				fatal(errors.New("only SCHED_CLS objects are permitted"))
			}
		}
		inventory[name] = map[string]interface{}{"sha256": digest, "maps": meta}
		specs[name] = spec
	}
	if !*execute {
		must(json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"mode": "ELF_parse_only_no_BPF_syscalls", "objects": inventory, "constants_rewritten_only_on_execution": constants}))
		return nil
	}
	if os.Geteuid() != 0 || os.Getenv("CUBE_ANONYMOUS_REVIEW") != "explicitly-approved-finite-fixture" {
		fatal(errors.New("execution requires root and separate explicit finite-fixture review"))
	}
	must(rlimit.RemoveMemlock()) // this process only; cgroup memory ceiling is required externally
	env := &environment{maps: map[string]*ebpf.Map{}, specs: specs, programs: map[string]*ebpf.Program{}, started: time.Now()}
	defer env.close()
	for _, name := range names {
		spec := specs[name]
		// All map FDs originate below. No PinPath, pinned map, existing map ID or link.
		for _, m := range spec.Maps {
			sanitize(m)
		}
		rewrites := map[string]interface{}{}
		for key, v := range constants {
			if _, ok := spec.Variables[key]; ok {
				rewrites[key] = v
			}
		}
		must(spec.RewriteConstants(rewrites))
		replacements := map[string]*ebpf.Map{}
		for _, key := range sharedNames {
			if spec.Maps[key] != nil && env.maps[key] != nil {
				replacements[key] = env.maps[key]
			}
		}
		col, err := ebpf.NewCollectionWithOptions(spec, ebpf.CollectionOptions{MapReplacements: replacements})
		must(err)
		env.coll = append(env.coll, col)
		for key, p := range col.Programs {
			if env.programs[key] != nil {
				fatal(errors.New("unexpected duplicate program name"))
			}
			env.programs[key] = p
		}
		for _, key := range sharedNames {
			if env.maps[key] == nil && col.Maps[key] != nil {
				env.maps[key] = col.Maps[key]
			}
		}
	}
	for _, key := range sharedNames {
		if env.maps[key] == nil {
			fatal(fmt.Errorf("required fresh map absent: %s", key))
		}
	}
	// Shape assertions are independent of Go native padding and validate pinned ABI.
	requireShape(env.maps["ifindex_to_mvmmeta"], 4, 128)
	requireShape(env.maps["baarcha_replies"], 12, 16)
	requireShape(env.maps["local_port_mapping"], 8, 2)
	requireShape(env.maps["remote_port_mapping"], 2, 8)
	results, err := env.cases()
	report := map[string]interface{}{"scope": "anonymous_kernel_decisions_only", "network_isolation_accepted": false, "production_rollout_authorized": false, "objects": inventory, "map_entry_cap": 64, "program_test_runs": env.calls, "cases": results, "duration_ms": time.Since(env.started).Milliseconds()}
	if err != nil {
		report["failure"] = err.Error()
	}
	must(json.NewEncoder(os.Stdout).Encode(report))
	// A zero process status means completed reporting, NOT 22 kernel passes.
	return err
}

type fixtureFailure struct{ error }

func fatal(err error) { panic(fixtureFailure{err}) }
func must(err error) {
	if err != nil {
		fatal(err)
	}
}
func sanitize(m *ebpf.MapSpec) {
	m.Pinning = ebpf.PinNone
	if m.MaxEntries > 64 {
		m.MaxEntries = 64
	}
	if m.InnerMap != nil {
		sanitize(m.InnerMap)
	}
}
func requireShape(m *ebpf.Map, k, v uint32) {
	if m.KeySize() != k || m.ValueSize() != v {
		fatal(errors.New("candidate map ABI mismatch"))
	}
}
func (e *environment) close() {
	for i := len(e.owned) - 1; i >= 0; i-- {
		e.owned[i].Close()
	}
	for i := len(e.coll) - 1; i >= 0; i-- {
		e.coll[i].Close()
	}
}
func (e *environment) run(name string, data []byte, ingress uint32) (uint32, []byte, error) {
	if e.calls >= 88 || time.Since(e.started) > 110*time.Second {
		return 0, nil, errors.New("finite run bound exceeded")
	}
	e.calls++
	p := e.programs[name]
	if p == nil {
		return 0, nil, errors.New("missing reviewed program")
	}
	context := make([]byte, 192)
	binary.LittleEndian.PutUint32(context[36:40], ingress)
	binary.LittleEndian.PutUint32(context[40:44], 1)
	output := make([]byte, 4096)
	// Repeat=1; library's SCHED_CLS syscall has flags/cpu/batch_size zero. No live-frame option.
	options := &ebpf.RunOptions{Data: data, DataOut: output, Context: context, Repeat: 1}
	ret, err := p.Run(options)
	return ret, options.DataOut, err
}
func (e *environment) reset() error {
	for _, name := range resetNames {
		m := e.maps[name]
		it := m.Iterate()
		key := make([]byte, m.KeySize())
		value := make([]byte, m.ValueSize())
		keys := [][]byte{}
		for it.Next(&key, &value) {
			keys = append(keys, append([]byte{}, key...))
			if len(keys) > 64 {
				return errors.New("unexpected fixture map growth")
			}
		}
		if err := it.Err(); err != nil {
			return err
		}
		for _, key := range keys {
			if err := m.Delete(key); err != nil {
				return err
			}
		}
	}
	if err := e.meta(guestA, 7); err != nil {
		return err
	}
	if err := e.meta(guestB, 7); err != nil {
		return err
	}
	for _, id := range []uint32{guestA, guestB} {
		for _, port := range []uint16{3000, 3031, 49983} {
			if err := e.maps["local_port_mapping"].Put(portKey(id, port), portBytes(53031)); err != nil {
				return err
			}
		}
	}
	if err := e.maps["remote_port_mapping"].Put(portBytes(53031), portKey(guestA, 3031)); err != nil {
		return err
	}
	return nil
}
func (e *environment) meta(id, version uint32) error {
	b := make([]byte, 128)
	binary.LittleEndian.PutUint32(b, version)
	addr := "198.18.0.10"
	if id == guestB {
		addr = "198.18.0.11"
	}
	binary.LittleEndian.PutUint32(b[4:], ip(addr))
	copy(b[8:72], "anonymous-fixture")
	binary.LittleEndian.PutUint32(b[76:], 17)
	if err := e.maps["ifindex_to_mvmmeta"].Put(id, b); err != nil {
		return err
	}
	return e.maps["mvmip_to_ifindex"].Put(ip(addr), id)
}
func (e *environment) grant(exposed bool) error {
	var data []byte
	name := "from_envoy"
	peer := "169.254.68.5"
	if exposed {
		name = "from_world"
		peer = "198.18.0.9"
		data = packet(peer, "198.18.0.1", 45000, 53031, 2, 6, 0, false)
	} else {
		data = packet("198.18.0.2", "198.18.0.10", 45000, 3031, 2, 6, 0, false)
	}
	ret, _, err := e.run(name, data, 1)
	if err != nil {
		return err
	}
	if ret != 7 {
		return fmt.Errorf("ingress positive-control verdict %d", ret)
	}
	value := make([]byte, 16)
	if err = e.maps["baarcha_replies"].Lookup(replyKey(guestA, peer, 45000, 3031), &value); err != nil {
		return fmt.Errorf("ingress failed to create permission: %w", err)
	}
	if binary.LittleEndian.Uint32(value[8:]) != 7 {
		return errors.New("wrong ingress permission generation")
	}
	return nil
}
func (e *environment) reply(dst string, id uint32, sport, dport uint16, flags byte, want string) error {
	ret, out, err := e.run("from_cube", packet("169.254.68.6", dst, sport, dport, flags, 6, 0, false), id)
	if err != nil {
		return err
	}
	if want == "drop" {
		if ret != 2 {
			return fmt.Errorf("expected hard drop, got %d", ret)
		}
		return nil
	}
	if ret != 7 || len(out) < 54 {
		return fmt.Errorf("expected valid reply redirect, got %d", ret)
	}
	expectedDst := dst
	if dst == "169.254.68.5" {
		expectedDst = "198.18.0.2"
	}
	if !bytes.Equal(out[30:34], addrBytes(expectedDst)) || out[47]&4 != 0 {
		return errors.New("positive control is not a valid reply; reset or wrong destination")
	}
	return nil
}
func (e *environment) classifier(cached bool) error {
	// A matching deny entry makes the public positive control depend on the
	// permissive allow entry, rather than accidentally passing by default.
	denySpec := e.specs["classifier_fixture.o"].Maps["deny_out"].InnerMap.Copy()
	sanitize(denySpec)
	deny, err := ebpf.NewMap(denySpec)
	if err != nil {
		return err
	}
	e.owned = append(e.owned, deny)
	if err = deny.Put(make([]byte, 8), uint32(1)); err != nil {
		return err
	}
	if err = e.maps["deny_out"].Put(guestA, deny); err != nil {
		return err
	}
	innerSpec := e.specs["classifier_fixture.o"].Maps["allow_out_v3"].InnerMap.Copy()
	sanitize(innerSpec)
	inner, err := ebpf.NewMap(innerSpec)
	if err != nil {
		return err
	}
	e.owned = append(e.owned, inner)
	key := make([]byte, 12)
	value := make([]byte, 16) // permissive0/0, non-expiring
	if err = inner.Put(key, value); err != nil {
		return err
	}
	if err = e.maps["allow_out_v3"].Put(guestA, inner); err != nil {
		return err
	}
	name := "fixture_classify"
	wantPublic, wantProtected := byte(1), byte(0)
	if cached {
		name = "fixture_cached"
		wantPublic, wantProtected = 0, 1
	}
	for _, test := range []struct {
		dst  string
		want byte
	}{{"203.0.113.9", wantPublic}, {"10.0.0.7", wantProtected}} {
		in := make([]byte, 24)
		binary.LittleEndian.PutUint32(in, guestA)
		copy(in[4:8], addrBytes(test.dst))
		binary.BigEndian.PutUint16(in[8:10], 443)
		in[10] = 6
		binary.LittleEndian.PutUint32(in[12:16], 17)
		binary.LittleEndian.PutUint32(in[16:20], 17)
		ret, out, err := e.run(name, in, guestA)
		if err != nil {
			return err
		}
		if ret != 0 || out[11] != test.want {
			return fmt.Errorf("classifier %s verdict/control mismatch", name)
		}
	}
	return nil
}
func (e *environment) cases() ([]result, error) {
	ids := []string{"reply-missing", "reply-positive", "reply-exposed-positive", "reply-unsolicited-synack", "reply-unsolicited-ack", "reply-unsolicited-rst", "reply-new-syn", "reply-guest-isolation", "reply-peer-mismatch", "reply-peer-port-mismatch", "reply-guest-port-mismatch", "reply-generation-change", "reply-expired", "reply-no-metadata", "reply-map-write-failure", "protected-allow-precedence", "protected-cached-session", "fragment-first", "fragment-following", "ethernet-ipv6", "encapsulation-ipip", "encapsulation-gre"}
	output := []result{}
	for _, id := range ids {
		before := e.calls
		r := result{ID: id, Status: "kernel_pass"}
		if err := e.reset(); err != nil {
			return output, err
		}
		var err error
		switch id {
		case "reply-missing":
			err = e.reply("169.254.68.5", guestA, 49983, 45000, 16, "drop")
		case "reply-unsolicited-ack":
			err = e.reply("169.254.68.5", guestA, 41000, 45000, 16, "drop")
		case "reply-unsolicited-synack":
			err = e.reply("169.254.68.5", guestA, 3031, 45000, 18, "drop")
		case "reply-unsolicited-rst":
			err = e.reply("169.254.68.5", guestA, 3000, 45000, 4, "drop")
		case "reply-positive", "reply-exposed-positive":
			exposed := id == "reply-exposed-positive"
			err = e.grant(exposed)
			if err == nil {
				dst := "169.254.68.5"
				if exposed {
					dst = "198.18.0.9"
				}
				err = e.reply(dst, guestA, 3031, 45000, 18, "allow")
			}
		case "reply-new-syn":
			err = e.grant(false)
			if err == nil {
				err = e.reply("169.254.68.5", guestA, 3031, 45000, 2, "drop")
			}
		case "reply-guest-isolation":
			err = e.grant(false)
			if err == nil {
				err = e.reply("169.254.68.5", guestB, 3031, 45000, 18, "drop")
			}
			if err == nil {
				err = e.reply("169.254.68.5", guestA, 3031, 45000, 18, "allow")
			}
		case "reply-peer-mismatch":
			err = e.grant(false)
			if err == nil {
				err = e.reply("169.254.68.7", guestA, 3031, 45000, 18, "drop")
			}
		case "reply-peer-port-mismatch":
			err = e.grant(false)
			if err == nil {
				err = e.reply("169.254.68.5", guestA, 3031, 45001, 18, "drop")
			}
		case "reply-guest-port-mismatch":
			err = e.grant(false)
			if err == nil {
				err = e.reply("169.254.68.5", guestA, 3000, 45000, 18, "drop")
			}
		case "reply-generation-change":
			err = e.grant(false)
			if err == nil {
				err = e.meta(guestA, 8)
			}
			if err == nil {
				err = e.reply("169.254.68.5", guestA, 3031, 45000, 18, "drop")
			}
			if err == nil {
				ret, _, x := e.run("from_envoy", packet("198.18.0.2", "198.18.0.10", 45000, 3031, 2, 6, 0, false), 1)
				err = x
				if err == nil && ret != 7 {
					err = errors.New("fresh generation ingress failed")
				}
			}
			if err == nil {
				err = e.reply("169.254.68.5", guestA, 3031, 45000, 18, "allow")
			}
		case "reply-expired":
			var now unix.Timespec
			err = unix.ClockGettime(unix.CLOCK_MONOTONIC, &now)
			ns := uint64(now.Sec)*1000000000 + uint64(now.Nsec)
			if err == nil && ns <= ttl+1 {
				r.Status = "kernel_inconclusive_userspace_clock_proof"
				r.Detail = "uptime below1h; no timestamp wrap, waiting or clock mutation"
			} else if err == nil {
				v := make([]byte, 16)
				binary.LittleEndian.PutUint64(v, ns-ttl-1)
				binary.LittleEndian.PutUint32(v[8:], 7)
				err = e.maps["baarcha_replies"].Put(replyKey(guestA, "169.254.68.5", 45000, 3031), v)
				if err == nil {
					err = e.reply("169.254.68.5", guestA, 3031, 45000, 18, "drop")
				}
			}
		case "reply-no-metadata":
			err = e.maps["ifindex_to_mvmmeta"].Delete(guestA)
			if err == nil {
				ret, _, x := e.run("from_envoy", packet("198.18.0.2", "198.18.0.10", 45000, 3031, 2, 6, 0, false), 1)
				err = x
				if err == nil && ret != 2 {
					err = errors.New("missing metadata ingress accepted")
				}
			}
			if err == nil {
				err = e.reply("169.254.68.5", guestA, 3031, 45000, 18, "drop")
			}
		case "reply-map-write-failure":
			r.Status = "userspace_helper_proof_only"
			r.Detail = "no kernel fault injection, map freezing or resource exhaustion proposed"
		case "protected-allow-precedence":
			err = e.classifier(false)
		case "protected-cached-session":
			err = e.classifier(true)
		default:
			fragment := uint16(0)
			protocol := byte(6)
			v6 := false
			switch id {
			case "fragment-first":
				fragment = 0x2000
			case "fragment-following":
				fragment = 1
			case "ethernet-ipv6":
				v6 = true
			case "encapsulation-ipip":
				protocol = 4
			case "encapsulation-gre":
				protocol = 47
			}
			ret, _, x := e.run("from_cube", packet("169.254.68.6", "10.0.0.7", 41000, 443, 2, protocol, fragment, v6), guestA)
			err = x
			if err == nil && ret != 2 {
				err = fmt.Errorf("nonstandard packet not dropped: %d", ret)
			}
		}
		r.Runs = e.calls - before
		if r.Runs > 4 {
			err = errors.New("per-case program-test bound exceeded")
		}
		if err != nil {
			r.Status = "kernel_fail"
			r.Detail = err.Error()
		}
		output = append(output, r)
		if err != nil {
			return output, err
		}
	}
	return output, nil
}
func ip(s string) uint32        { a := netip.MustParseAddr(s).As4(); return binary.LittleEndian.Uint32(a[:]) }
func addrBytes(s string) []byte { a := netip.MustParseAddr(s).As4(); return a[:] }
func portBytes(p uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, p); return b }
func portKey(id uint32, port uint16) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, id)
	binary.BigEndian.PutUint16(b[4:], port)
	return b
}
func replyKey(id uint32, peer string, peerPort, guestPort uint16) []byte {
	b := make([]byte, 12)
	binary.LittleEndian.PutUint32(b, id)
	copy(b[4:8], addrBytes(peer))
	binary.BigEndian.PutUint16(b[8:], peerPort)
	binary.BigEndian.PutUint16(b[10:], guestPort)
	return b
}
func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i < len(b); i += 2 {
		sum += uint32(b[i]) << 8
		if i+1 < len(b) {
			sum += uint32(b[i+1])
		}
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}
func packet(src, dst string, sport, dport uint16, flags, protocol byte, fragment uint16, v6 bool) []byte {
	eth, _ := hex.DecodeString("20906fcfcfcf20906ffcfcfc0800")
	payload := make([]byte, 20)
	binary.BigEndian.PutUint16(payload, sport)
	binary.BigEndian.PutUint16(payload[2:], dport)
	binary.BigEndian.PutUint32(payload[4:], 1)
	binary.BigEndian.PutUint32(payload[8:], 1)
	payload[12] = 0x50
	payload[13] = flags
	binary.BigEndian.PutUint16(payload[14:], 8192)
	if v6 {
		eth[12], eth[13] = 0x86, 0xdd
		header := make([]byte, 40)
		header[0] = 0x60
		binary.BigEndian.PutUint16(header[4:], uint16(len(payload)))
		header[6], header[7] = 6, 64
		a := netip.MustParseAddr("2001:db8::10").As16()
		b := netip.MustParseAddr("2001:db8::20").As16()
		copy(header[8:], a[:])
		copy(header[24:], b[:])
		pseudo := append(append([]byte{}, header[8:40]...), 0, 0, 0, 20, 0, 0, 0, 6)
		binary.BigEndian.PutUint16(payload[16:], checksum(append(pseudo, payload...)))
		return append(append(eth, header...), payload...)
	}
	header := make([]byte, 20)
	header[0] = 0x45
	binary.BigEndian.PutUint16(header[2:], 40)
	binary.BigEndian.PutUint16(header[4:], 431)
	binary.BigEndian.PutUint16(header[6:], fragment)
	header[8], header[9] = 64, protocol
	copy(header[12:], addrBytes(src))
	copy(header[16:], addrBytes(dst))
	binary.BigEndian.PutUint16(header[10:], checksum(header))
	if protocol == 6 {
		pseudo := append(append([]byte{}, header[12:20]...), 0, 6, 0, 20)
		binary.BigEndian.PutUint16(payload[16:], checksum(append(pseudo, payload...)))
	}
	return append(append(eth, header...), payload...)
}
