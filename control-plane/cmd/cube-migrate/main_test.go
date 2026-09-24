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

func TestStatusReadsLegacyJournalWithoutApplyingSchema(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "legacy-journal.db")
	db, e := sql.Open("sqlite3", database)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`CREATE TABLE runtime_migration(sandbox_id TEXT,phase TEXT,template_id TEXT,runtime_id TEXT,archive_sha256 TEXT,rollback_sha256 TEXT)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO runtime_migration VALUES ('fixture','rollback_started','template','remote','source-digest','')`); e != nil {
		t.Fatal(e)
	}
	if e = run([]string{"--database", database, "status"}); e != nil {
		t.Fatal(e)
	}
	var columns int
	if e = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('runtime_migration')`).Scan(&columns); e != nil || columns != 6 {
		t.Fatal("status mutated old recovery journal", e, columns)
	}
}
