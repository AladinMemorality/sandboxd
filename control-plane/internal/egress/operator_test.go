package egress

import (
	"net/netip"
	"testing"
)

func TestOperatorPolicyProtectsExplicitAndServiceAddresses(t *testing.T) {
	p, e := OperatorPolicy("192.0.2.1/32", "management.example.com", "https://8.8.8.8", "https://relay.example.com/api/bridge")
	if e != nil {
		t.Fatal(e)
	}
	if p.public(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("service address allowed")
	}
	found := false
	for _, d := range p.ProtectedDomains {
		if d == "relay.example.com" {
			found = true
		}
	}
	if !found {
		t.Fatal("service hostname unprotected")
	}
	for _, v := range []string{"", "::1/128", "not-a-prefix"} {
		if _, e = OperatorPolicy(v, "management.example.com"); e == nil {
			t.Fatal("invalid CIDRs accepted")
		}
	}
}
