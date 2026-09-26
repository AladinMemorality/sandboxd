package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchInputRefusesUnsafeShapesBeforeDatabaseOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "batch.json")
	db := filepath.Join(root, "absent.db")
	rows := []batchProject{{SandboxID: "01M2QR4FCP4VR4G6367E4A8JN4", Preset: "react-pro"}, {SandboxID: "01M2JJKKPBEN19SETC51QT34Y3", Preset: "react-pro"}}
	raw, _ := json.Marshal(map[string]any{"version": 1, "projects": rows})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	fleet := strings.Repeat("a", 64)
	for _, action := range []string{"migrate", "resume"} {
		got, err := readBatch(path, action, "", "", fleet, action == "migrate")
		if err != nil || len(got) != 2 {
			t.Fatal(got, err)
		}
	}
	for _, args := range [][]string{
		{"--batch", path, "--expected-fleet", fleet, "migrate"},
		{"--batch", path, "--expected-fleet", fleet, "rollback"},
		{"--batch", path, "--sandbox", rows[0].SandboxID, "--expected-fleet", fleet, "resume"},
		{"--batch", path, "resume"},
	} {
		if err := run(append([]string{"--database", db}, args...)); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
	for _, bad := range []any{
		map[string]any{"version": 1, "projects": append(rows, rows[0])},
		map[string]any{"version": 1, "projects": []batchProject{}},
		map[string]any{"version": 1, "projects": rows, "unknown": true},
		map[string]any{"version": 2, "projects": rows},
	} {
		raw, _ = json.Marshal(bad)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readBatch(path, "resume", "", "", fleet, false); err == nil {
			t.Fatal("invalid input accepted", bad)
		}
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("invalid input touched database", err)
	}
}
