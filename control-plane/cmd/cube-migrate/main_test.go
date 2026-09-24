package main

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestAdmissionStatusIsReadOnlyAndDoesNotRequireProviderConfig(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "admission.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE cube_admission(admission_key TEXT,runtime_id TEXT,template_id TEXT,operation TEXT,state TEXT,charged INTEGER,token TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cube_admission VALUES('app:fixture','vm-fixture','tpl-fixture','connect','pending',1,'DO_NOT_PRINT_OPERATION')`); err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	runErr := run([]string{"--database", database, "admission-status"})
	os.Stdout = originalStdout
	writer.Close()
	output, readErr := io.ReadAll(reader)
	reader.Close()
	if runErr != nil || readErr != nil {
		t.Fatal(runErr, readErr)
	}
	if strings.Contains(string(output), "DO_NOT_PRINT_OPERATION") || !strings.Contains(string(output), "app:fixture") {
		t.Fatal("admission status exposed marker or omitted identity")
	}

	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("status applied migrations", err, count)
	}
	if _, err = os.Stat(database + ".maintenance.lock"); !os.IsNotExist(err) {
		t.Fatal("status created mutation fence", err)
	}
}
func TestAdmissionReconcileRequiresExplicitProviderDrainBeforeMutation(t *testing.T) {
	database := filepath.Join(t.TempDir(), "missing.db")
	if err := run([]string{"--database", database, "--admission-key", "app:fixture", "admission-reconcile"}); err == nil {
		t.Fatal("reconciliation allowed without provider request-drain fence")
	}
	if _, err := os.Stat(database); !os.IsNotExist(err) {
		t.Fatal("refused command mutated database", err)
	}
}
