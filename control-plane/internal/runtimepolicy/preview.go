// Package runtimepolicy identifies accounts whose compute admission belongs to
// the upstream platform. Preview credentials grant access, never permission to
// allocate compute after a sandbox has stopped.
package runtimepolicy

import (
	"os"
	"strings"
)

func RequiresExplicitStart(owner string) bool {
	prefixes, configured := os.LookupEnv("SANDBOXD_EXPLICIT_WAKE_PREFIXES")
	if !configured {
		prefixes = "baarcha:"
	}
	for _, prefix := range strings.Split(prefixes, ",") {
		prefix = strings.TrimSpace(prefix)
		if prefix != "" && strings.HasPrefix(owner, prefix) {
			return true
		}
	}
	return false
}
