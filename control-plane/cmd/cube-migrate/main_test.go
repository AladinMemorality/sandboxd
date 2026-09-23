package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestInventoryAcceptsLegacySchemaWithoutMutatingIt(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "legacy.db")
	workspaces := filepath.Join(root, "workspaces")
	home := filepath.Join(workspaces, "fixture")
	if err := os.MkdirAll(filepath.Join(home, "workspace", "app"), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{`CREATE TABLE sandbox(id TEXT,app_id TEXT,status TEXT,container_id TEXT,workspace_mnt TEXT)`, `CREATE TABLE task(task_id TEXT,sandbox_id TEXT,status TEXT)`} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO sandbox VALUES ('fixture','app','stopped','container',?)`, home); err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"--database", database, "--workspaces", workspaces, "inventory"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("read-only inventory applied schema migrations", err, count)
	}
	if _, err = os.Stat(database + ".maintenance.lock"); !os.IsNotExist(err) {
		t.Fatal("inventory created mutation lock", err)
	}
}
