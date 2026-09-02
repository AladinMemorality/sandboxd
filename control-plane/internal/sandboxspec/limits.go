package sandboxspec

import (
	"os"
	"strings"
)

// Limits are the per-container resource ceilings. One place for both
// creation paths (the API create handler and the recreate-on-wake path in
// main), so a sandbox comes back after an upgrade with the same limits it was
// born with.
//
// Defaults keep the historical behaviour (10 GiB, no CPU cap) so an install
// that never set the env sees no change. A multi-tenant host wants far less:
// on a 64 GiB box meant to hold a hundred-plus sandboxes, 10 GiB each is a
// promise the box cannot keep, and one runaway build starves everyone.
type Limits struct {
	Memory string // --memory and --memory-swap (no swap beyond the ceiling), e.g. "2g"
	CPUs   string // --cpus, e.g. "2"; "" = unlimited
}

// DefaultLimits is what an unset environment gets.
var DefaultLimits = Limits{Memory: "10g", CPUs: ""}

// LimitsFromEnv reads SANDBOXD_SANDBOX_MEMORY and SANDBOXD_SANDBOX_CPUS,
// falling back to DefaultLimits field by field.
func LimitsFromEnv() Limits {
	l := DefaultLimits
	if v := strings.TrimSpace(os.Getenv("SANDBOXD_SANDBOX_MEMORY")); v != "" {
		l.Memory = v
	}
	if v := strings.TrimSpace(os.Getenv("SANDBOXD_SANDBOX_CPUS")); v != "" && v != "0" {
		l.CPUs = v
	}
	return l
}
