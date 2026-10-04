package runtimepolicy

import "testing"

func TestManagedOwnerPrefixes(t *testing.T) {
	t.Setenv("SANDBOXD_EXPLICIT_WAKE_PREFIXES", "baarcha:, platform:")
	for owner, want := range map[string]bool{"baarcha:42": true, "platform:7": true, "owner-one": false, "": false} {
		if got := RequiresExplicitStart(owner); got != want {
			t.Fatalf("%s: %v", owner, got)
		}
	}
	t.Setenv("SANDBOXD_EXPLICIT_WAKE_PREFIXES", "")
	if RequiresExplicitStart("baarcha:42") {
		t.Fatal("empty configuration should disable managed prefixes")
	}
}
