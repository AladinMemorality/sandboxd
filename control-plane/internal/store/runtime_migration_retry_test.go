package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMigrationHomeManifestVersionBounds(t *testing.T) {
	for _, v := range []struct {
		version int
		size    int
		ok      bool
	}{{1, 5000, false}, {2, 5000, true}, {2, 33000, false}} {
		s, id := migrationFixture(t)
		raw, e := json.Marshal(map[string]any{"version": v.version, "data": strings.Repeat("x", v.size)})
		if e != nil {
			t.Fatal(e)
		}
		e = s.BeginRuntimeMigrationWithHome(context.Background(), id, "vite", "template", "cube.local", string(raw))
		if (e == nil) != v.ok {
			t.Fatalf("version=%d size=%d: %v", v.version, v.size, e)
		}
	}
}

func TestReplanRetainsCompleteJournalAndRefreezesTasks(t *testing.T) {
	s, id := migrationFixture(t)
	ctx := context.Background()
	for _, name := range []string{"before"} {
		if e := s.CreateTask(ctx, &Task{TaskID: name, SandboxID: id, Agent: "claude-code", Prompt: "private"}); e != nil {
			t.Fatal(e)
		}
		if e := s.FinishTask(ctx, name, "succeeded", "{}"); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.BeginRuntimeMigrationWithHome(ctx, id, "vite", "first", "cube.local", "{}"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE runtime_migration SET token_ciphertext=?,token_nonce=?,archive_sha256='original' WHERE sandbox_id=?`, []byte("sealed-private"), []byte("nonce"), id); e != nil {
		t.Fatal(e)
	}
	if e := s.AbortRuntimeMigration(ctx, id); e != nil {
		t.Fatal(e)
	}
	if e := s.CreateTask(ctx, &Task{TaskID: "after", SandboxID: id, Agent: "claude-code", Prompt: "new work"}); e != nil {
		t.Fatal(e)
	}
	if e := s.ReplanAbortedRuntimeMigrationWithHome(ctx, id, "vite", "second", "cube.local", "{}"); e == nil {
		t.Fatal("active task accepted")
	}
	if e := s.FinishTask(ctx, "after", "succeeded", "{}"); e != nil {
		t.Fatal(e)
	}
	if e := s.ReplanAbortedRuntimeMigrationWithHome(ctx, id, "vite", "second", "cube.local", "{}"); e != nil {
		t.Fatal(e)
	}
	m, e := s.GetRuntimeMigration(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if m.Phase != "planned" || len(m.ArchiveGeneration) != 32 || m.Binding.RuntimeID != "" || len(m.Binding.TokenCiphertext) != 0 || m.ArchiveSHA256 != "" || m.Binding.TemplateID != "second" {
		t.Fatalf("new attempt not fresh: %+v", m)
	}
	var journal, tasks string
	if e = s.db.QueryRow(`SELECT journal_json,task_ids_json FROM runtime_migration_attempt WHERE sandbox_id=? AND archive_generation=''`, id).Scan(&journal, &tasks); e != nil {
		t.Fatal(e)
	}
	var audit map[string]any
	if e = json.Unmarshal([]byte(journal), &audit); e != nil {
		t.Fatal(e)
	}
	if audit["phase"] != "aborted" || audit["archive_sha256"] != "original" || audit["template_id"] != "first" || audit["token_ciphertext"] != "c2VhbGVkLXByaXZhdGU=" || tasks != "[\"before\"]" {
		t.Fatalf("retained journal incomplete: %s %s", journal, tasks)
	}
	var n int
	if e = s.db.QueryRow(`SELECT count(*) FROM runtime_migration_task WHERE sandbox_id=?`, id).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	generation := m.ArchiveGeneration
	if e = s.AbortRuntimeMigration(ctx, id); e != nil {
		t.Fatal(e)
	}
	if e = s.ReplanAbortedRuntimeMigrationWithHome(ctx, id, "vite", "third", "cube.local", "{}"); e != nil {
		t.Fatal(e)
	}
	m, e = s.GetRuntimeMigration(ctx, id)
	if e != nil || m.ArchiveGeneration == generation {
		t.Fatal("archive generation reused", e)
	}
	if e = s.db.QueryRow(`SELECT count(*) FROM runtime_migration_attempt WHERE sandbox_id=?`, id).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
}

func TestReplanRequiresAbortedUnboundDeletedTargetAndOriginalSource(t *testing.T) {
	for _, fault := range []string{"planned", "staging", "complete", "target-not-deleted", "changed-source"} {
		t.Run(fault, func(t *testing.T) {
			s, id := migrationFixture(t)
			ctx := context.Background()
			if e := s.BeginRuntimeMigration(ctx, id, "vite", "template", "cube.local"); e != nil {
				t.Fatal(e)
			}
			phase := "aborted"
			if fault == "planned" || fault == "staging" || fault == "complete" {
				phase = fault
			}
			if _, e := s.db.Exec(`UPDATE runtime_migration SET phase=? WHERE sandbox_id=?`, phase, id); e != nil {
				t.Fatal(e)
			}
			if fault == "target-not-deleted" {
				if _, e := s.db.Exec(`UPDATE runtime_migration SET runtime_id='unacknowledged' WHERE sandbox_id=?`, id); e != nil {
					t.Fatal(e)
				}
			}
			if fault == "changed-source" {
				if _, e := s.db.Exec(`UPDATE sandbox SET container_id='replacement' WHERE id=?`, id); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.ReplanAbortedRuntimeMigrationWithHome(ctx, id, "vite", "new", "cube.local", ""); e == nil {
				t.Fatal("unsafe replan accepted")
			}
			var n int
			if e := s.db.QueryRow(`SELECT count(*) FROM runtime_migration_attempt`).Scan(&n); e != nil || n != 0 {
				t.Fatal("failed transaction changed audit", n, e)
			}
			m, e := s.GetRuntimeMigration(ctx, id)
			if e != nil || m.Phase != phase {
				t.Fatal("old journal lost", e)
			}
		})
	}
}

func TestReplanAcceptsOnlyAcknowledgedDeletedAdmission(t *testing.T) {
	s, id := migrationFixture(t)
	ctx := context.Background()
	if e := s.BeginRuntimeMigration(ctx, id, "vite", "template", "cube.local"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE runtime_migration SET phase='aborted',runtime_id='former' WHERE sandbox_id=?`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`INSERT INTO cube_admission(admission_key,runtime_id,template_id,operation,token,state,charged) VALUES('app:migration-app','former','template','delete','ack','deleted',0)`); e != nil {
		t.Fatal(e)
	}
	if e := s.ReplanAbortedRuntimeMigrationWithHome(ctx, id, "vite", "template", "cube.local", ""); e != nil {
		t.Fatal(e)
	}
	var state string
	if e := s.db.QueryRow(`SELECT state FROM cube_admission WHERE runtime_id='former'`).Scan(&state); e != nil || state != "deleted" {
		t.Fatal("replan modified provider admission", state, e)
	}
}
