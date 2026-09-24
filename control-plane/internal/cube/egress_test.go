package cube

import (
	"errors"
	"reflect"
	"testing"
)

func TestOperatorEgressRemainsDenyAll(t *testing.T) {
	p, err := OperatorEgressPolicy("")
	if err != nil || p.AllowPublicTraffic || len(p.AllowOut) != 0 || !reflect.DeepEqual(p.DenyOut, []string{"0.0.0.0/0"}) {
		t.Fatalf("unsafe default: %+v %v", p, err)
	}
	p.DenyOut[0] = "mutated"
	next, _ := OperatorEgressPolicy("")
	if next.DenyOut[0] != "0.0.0.0/0" {
		t.Fatal("caller mutated shared policy")
	}
}

func TestOperatorEgressRejectsUnsupportedAllowances(t *testing.T) {
	for _, input := range []string{"registry.npmjs.org", "registry.npmjs.org,pypi.org", "relay.example.com"} {
		p, err := OperatorEgressPolicy(input)
		if p != nil || !errors.Is(err, ErrUnsafeDomainEgress) {
			t.Fatalf("unverified domain egress enabled: %q %+v %v", input, p, err)
		}
	}
	for _, input := range []string{"*", "*.npmjs.org", "0.0.0.0/0", "127.0.0.1", "::/0", "https://registry.npmjs.org", "registry.npmjs.org:443", "registry.npmjs.org,", "localhost", "bad..example.com"} {
		if p, err := OperatorEgressPolicy(input); p != nil || err == nil || errors.Is(err, ErrUnsafeDomainEgress) {
			t.Errorf("invalid allow input not rejected specifically: %q %v", input, err)
		}
	}
}
