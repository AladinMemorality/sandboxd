package store

import (
	"context"
	"database/sql"
	"testing"
)

func migrationFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	app := &App{ID: "migration-app", OwnerToken: "owner", Name: "private", RuntimePreset: sql.NullString{String: "vite", Valid: true}}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	sb := &Sandbox{ID: "stable-sandbox", Status: "stopped", AppID: sql.NullString{String: app.ID, Valid: true}, Image: "original-image", ContainerID: sql.NullString{String: "original-container", Valid: true}, Ports: []int{3000}, Visibility: "private"}
	if err := s.Create(ctx, sb); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRunning(ctx, sb.ID, "original-container", "original-cgroup"); err != nil {
		t.Fatal(err)
	}
	return s, sb.ID
}

func TestMigrationAtomicIdentityAndCurrentDataRollback(t *testing.T) {
	s, id := migrationFixture(t)
	ctx := context.Background()
	if err := s.BeginRuntimeMigration(ctx, id, "vite", "trusted-template", "cube.local"); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.HasIncompleteRuntimeMigrations(ctx); err != nil || !pending {
		t.Fatal("missing maintenance fence", err)
	}
	if err := s.CommitRuntimeMigration(ctx, id); err == nil {
		t.Fatal("unverified cutover accepted")
	}
	for _, step := range [][2]string{{"planned", "quiesced"}, {"quiesced", "archived"}} {
		if err := s.AdvanceRuntimeMigration(ctx, id, step[0], step[1], "digest"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PrepareMigrationTargetCredential(ctx, id, []byte("encrypted"), []byte("nonce")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMigrationTarget(ctx, id, &RuntimeBinding{RuntimeID: "remote", TokenCiphertext: []byte("sealed"), TokenNonce: []byte("nonce")}); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{{"staged", "imported"}, {"imported", "verified"}} {
		if err := s.AdvanceRuntimeMigration(ctx, id, step[0], step[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CommitRuntimeMigration(ctx, id); err != nil {
		t.Fatal(err)
	}
	sb, err := s.Get(ctx, id)
	if err != nil || sb.RuntimeProvider != "cube" || sb.AppID.String != "migration-app" || sb.Ports[0] != 3000 || sb.Visibility != "private" {
		t.Fatalf("identity lost: %+v %v", sb, err)
	}
	m, err := s.GetRuntimeMigration(ctx, id)
	if err != nil || m.Source.ContainerID.String != "original-container" {
		t.Fatal("source retention", err)
	}
	if err := s.CommitRuntimeRollback(ctx, id); err == nil {
		t.Fatal("stale data rollback accepted")
	}
	if err := s.CreateTask(ctx, &Task{TaskID: "cube-finished", SandboxID: id, Agent: "claude-code", Prompt: "Cube task"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishTask(ctx, "cube-finished", "succeeded", "{}"); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{{"complete", "rollback_started"}, {"rollback_started", "rollback_archived"}, {"rollback_archived", "rollback_restored"}} {
		if err := s.AdvanceRuntimeMigration(ctx, id, step[0], step[1], "new-data-digest"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CommitRuntimeRollback(ctx, id); err != nil {
		t.Fatal(err)
	}
	sb, err = s.Get(ctx, id)
	if err != nil || sb.RuntimeProvider != "docker" || sb.ContainerID.String != "original-container" || sb.Image != "original-image" {
		t.Fatalf("rollback identity %+v %v", sb, err)
	}
	if _, err = s.GetRuntimeBinding(ctx, id); err != ErrNotFound {
		t.Fatal("stale Cube binding", err)
	}
	assertFresh := func(agent string, want bool) {
		t.Helper()
		got, err := s.RollbackNeedsFreshAgentSession(ctx, id, agent)
		if err != nil || got != want {
			t.Fatalf("fresh %s = %v, want %v: %v", agent, got, want, err)
		}
	}
	assertFresh("claude-code", true) // Successful imported Cube history is not a local conversation.
	if err := s.CreateTask(ctx, &Task{TaskID: "new-local", SandboxID: id, Agent: "claude-code", Prompt: "local task"}); err != nil {
		t.Fatal(err)
	}
	assertFresh("claude-code", true) // Submission or lost acknowledgement does not consume the guard.
	if err := s.FinishTask(ctx, "new-local", "failed", "{}"); err != nil {
		t.Fatal(err)
	}
	assertFresh("claude-code", true)
	if err := s.CreateTask(ctx, &Task{TaskID: "new-success", SandboxID: id, Agent: "claude-code", Prompt: "local task"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishTask(ctx, "new-success", "succeeded", "{}"); err != nil {
		t.Fatal(err)
	}
	assertFresh("claude-code", false)
	assertFresh("opencode", true) // Each provider maintains a separate local session.
	if pending, err := s.HasIncompleteRuntimeMigrations(ctx); err != nil || pending {
		t.Fatal("maintenance fence not released", err)
	}
}

func TestMigrationRejectsActiveTasksAndDuplicateJournals(t *testing.T) {
	s, id := migrationFixture(t)
	ctx := context.Background()
	if err := s.CreateTask(ctx, &Task{TaskID: "task", SandboxID: id, Agent: "claude", Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRuntimeMigration(ctx, id, "vite", "template", "cube.local"); err == nil {
		t.Fatal("active task accepted")
	}
	if err := s.FinishTask(ctx, "task", "succeeded", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRuntimeMigration(ctx, id, "vite", "template", "cube.local"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRuntimeMigration(ctx, id, "vite", "template", "cube.local"); err == nil {
		t.Fatal("existing recovery journal overwritten")
	}
	if err := s.AdvanceRuntimeMigration(ctx, id, "planned", "complete", ""); err == nil {
		t.Fatal("invalid state transition")
	}
}
