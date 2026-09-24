package cubevs

import (
	"testing"

	"github.com/cilium/ebpf"
)

// Loading fresh anonymous maps/programs checks the real full dataplane against
// this kernel's verifier without attaching a program or reusing worker pins.
func TestBaarchaFullDataplaneVerifier(t *testing.T) {
	loaders := map[string]func() (*ebpf.CollectionSpec, error){
		"mvmtap": loadMvmtap,
		"nodenic": loadNodenic,
		"localgw": loadLocalgw,
	}
	for name, load := range loaders {
		t.Run(name, func(t *testing.T) {
			spec, err := load()
			if err != nil { t.Fatal(err) }
			for _, m := range spec.Maps {
				m.Pinning = ebpf.PinNone
				if m.InnerMap != nil { m.InnerMap.Pinning = ebpf.PinNone }
			}
			collection, err := ebpf.NewCollection(spec)
			if err != nil { t.Fatalf("full %s dataplane verifier: %+v", name, err) }
			collection.Close()
		})
	}
}
