//go:build linux

package main

import (
	"encoding/binary"
	"github.com/cilium/ebpf"
	"testing"
)

// Pure byte checks only: these tests never create maps or invoke BPF.
func TestSyntheticPacketChecksums(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		p := packet("169.254.68.6", "169.254.68.5", 3031, 45000, 18, 6, 0, v6)
		var tcp, pseudo []byte
		if v6 {
			if len(p) != 74 || binary.BigEndian.Uint16(p[12:14]) != 0x86dd {
				t.Fatal("bad IPv6 fixture")
			}
			tcp = p[54:]
			pseudo = append(append([]byte{}, p[22:54]...), 0, 0, 0, 20, 0, 0, 0, 6)
		} else {
			if len(p) != 54 || checksum(p[14:34]) != 0 {
				t.Fatal("bad IPv4 fixture checksum")
			}
			tcp = p[34:]
			pseudo = append(append([]byte{}, p[26:34]...), 0, 6, 0, 20)
		}
		if checksum(append(pseudo, tcp...)) != 0 {
			t.Fatal("bad TCP fixture checksum")
		}
		if binary.BigEndian.Uint16(tcp[:2]) != 3031 || binary.BigEndian.Uint16(tcp[2:4]) != 45000 || tcp[13] != 18 {
			t.Fatal("fixture tuple mismatch")
		}
	}
}
func TestFixtureTupleABIs(t *testing.T) {
	k := replyKey(guestA, "169.254.68.5", 45000, 3031)
	if len(k) != 12 || binary.LittleEndian.Uint32(k[:4]) != guestA || binary.BigEndian.Uint16(k[8:10]) != 45000 || binary.BigEndian.Uint16(k[10:]) != 3031 {
		t.Fatal("reply key ABI")
	}
	p := portKey(guestA, 3031)
	if len(p) != 8 || binary.LittleEndian.Uint32(p[:4]) != guestA || binary.BigEndian.Uint16(p[4:6]) != 3031 || p[6] != 0 || p[7] != 0 {
		t.Fatal("port key ABI")
	}
}
func TestAnonymousMapSanitization(t *testing.T) {
	spec := &ebpf.MapSpec{Type: ebpf.HashOfMaps, MaxEntries: 8192, Pinning: ebpf.PinByName, KeySize: 4, ValueSize: 4, InnerMap: &ebpf.MapSpec{Type: ebpf.LPMTrie, MaxEntries: 65536, Pinning: ebpf.PinByName, KeySize: 12, ValueSize: 16}}
	sanitize(spec)
	if spec.Pinning != ebpf.PinNone || spec.MaxEntries != 64 || spec.InnerMap.Pinning != ebpf.PinNone || spec.InnerMap.MaxEntries != 64 || spec.InnerMap.KeySize != 12 || spec.InnerMap.ValueSize != 16 {
		t.Fatal("map must remain bounded/unpinned with original ABI")
	}
}
