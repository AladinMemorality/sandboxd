package migration

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetryArchivesCannotOverwriteOriginalRecoveryFiles(t *testing.T) {
	b := &OfflineBackend{ArchiveDir: t.TempDir()}
	m := &store.RuntimeMigration{SandboxID: "owned-source"}
	for _, name := range []string{"source", "source-home", "source-history", "verified", "rollback-home"} {
		old, e := b.artifact(m, name)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(old, []byte("retained original"), 0600); e != nil {
			t.Fatal(e)
		}
		retry := *m
		retry.ArchiveGeneration = strings.Repeat("a", 32)
		fresh, e := b.artifact(&retry, name)
		if e != nil {
			t.Fatal(e)
		}
		if fresh == old || filepath.Dir(fresh) == filepath.Dir(old) {
			t.Fatal("archive collision")
		}
		if e = os.WriteFile(fresh, []byte("new attempt"), 0600); e != nil {
			t.Fatal(e)
		}
		raw, e := os.ReadFile(old)
		if e != nil || string(raw) != "retained original" {
			t.Fatal("original changed", e)
		}
	}
	m.ArchiveGeneration = "../other"
	if _, e := b.artifact(m, "source"); e == nil {
		t.Fatal("archive generation traversal accepted")
	}
}
