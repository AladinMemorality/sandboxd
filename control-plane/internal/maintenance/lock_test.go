package maintenance

import (
	"path/filepath"
	"testing"
)

func TestDaemonAndMigrationExcludeEachOther(t *testing.T) {
	db := filepath.Join(t.TempDir(), "sandboxd.db")
	daemon, err := Acquire(db, false)
	if err != nil {
		t.Fatal(err)
	}
	if migration, err := Acquire(db, true); err == nil {
		migration.Close()
		t.Fatal("migration entered live daemon")
	}
	daemon.Close()
	migration, err := Acquire(db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer migration.Close()
	if daemon, err := Acquire(db, false); err == nil {
		daemon.Close()
		t.Fatal("daemon entered migration")
	}
}
