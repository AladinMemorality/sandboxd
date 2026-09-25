package main

import (
	"context"
	"encoding/json"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/recovery"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ownedEvidence() (escrow, journalHandoff) {
	old := escrow{Guest: &cube.Sandbox{SandboxID: strings.Repeat("a", 32), TemplateID: template}, Fixture: strings.Repeat("b", 32), WorkerMachineID: strings.Repeat("c", 32), Latest: marker{Fixture: strings.Repeat("b", 32), Phase: "latest", Nonce: strings.Repeat("d", 32)}}
	h := journalHandoff{Purpose: "DISPOSABLE_CURRENT_DISK_JOURNAL", Fixture: old.Fixture, OldID: old.Guest.SandboxID, Machine: old.WorkerMachineID, Archive: strings.Repeat("e", 64), Evidence: strings.Repeat("f", 64), Expires: 1010}
	return old, h
}
func TestJournalHandoffBindsSourceAndExpires(t *testing.T) {
	old, h := ownedEvidence()
	if e := checkHandoff(h, old, 1000); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*journalHandoff){func(h *journalHandoff) { h.OldID = "other" }, func(h *journalHandoff) { h.Fixture = "other" }, func(h *journalHandoff) { h.Machine = "other" }, func(h *journalHandoff) { h.Expires = 1000 }, func(h *journalHandoff) { h.Expires = 2201 }, func(h *journalHandoff) { h.Archive = "" }, func(h *journalHandoff) { h.Purpose = "DISPOSABLE_CURRENT_DISK_REPLACEMENT" }} {
		v := h
		mutate(&v)
		if checkHandoff(v, old, 1000) == nil {
			t.Fatal("unsafe handoff accepted")
		}
	}
}
func TestJournalSeedIsIsolatedAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	stage := t.TempDir()
	dbPath := filepath.Join(stage, "fixture.db")
	key, e := secrets.Load("", filepath.Join(stage, "key"))
	if e != nil {
		t.Fatal(e)
	}
	old, _ := ownedEvidence()
	old.Supervisor = "fixture-supervisor"
	old.Guest.TrafficAccessToken = "fixture-traffic"
	seedDatabase(ctx, dbPath, "../../migrations", key, old, "fixture-app", "fixture-sandbox", "fixture-owner")
	db := openFixture(ctx, dbPath, "../../migrations")
	defer db.Close()
	app, e := db.GetAppForOwner(ctx, "fixture-app", "fixture-owner")
	if e != nil || app.ExternalProjectID.String != "journal-fixture-project" {
		t.Fatal("owner identity missing", e)
	}
	task, e := db.GetTask(ctx, syntheticTask)
	if e != nil || task.Status != "succeeded" || !strings.Contains(task.Prompt, "never ran") {
		t.Fatal("synthetic task scope missing", e)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("existing database overwritten")
			}
		}()
		seedDatabase(ctx, dbPath, "../../migrations", key, old, "wrong", "wrong", "wrong")
	}()
}
func TestJournalFixtureHistoryIsCanonicalAndSynthetic(t *testing.T) {
	stage := t.TempDir()
	data := fixtureHistory(context.Background(), stage)
	if len(data) == 0 {
		t.Fatal("empty fixture")
	}
	raw, e := os.ReadFile(filepath.Join(stage, "synthetic-task-source", syntheticTask, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result["checkpoint_id"] != "synthetic-journal-only" {
		t.Fatal("history provenance")
	}
}
func TestJournalProbeOnlyVerifiesOriginalLatest(t *testing.T) {
	old, h := ownedEvidence()
	old.Baseline = old.Latest
	if checkHandoff(h, old, 1000) == nil {
		t.Fatal("old checkpoint presented as latest")
	}
}

func TestJournalSeedEntersRealOfflineRecoveryAndRequiresVerification(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native root offline session fixture")
	}
	ctx := context.Background()
	stage := t.TempDir()
	path := filepath.Join(stage, "fixture.db")
	key, e := secrets.Load("", filepath.Join(stage, "key"))
	if e != nil {
		t.Fatal(e)
	}
	old, _ := ownedEvidence()
	old.Supervisor = "synthetic"
	old.Guest.TrafficAccessToken = "synthetic-traffic"
	seedDatabase(ctx, path, "../../migrations", key, old, "fixture-app", "fixture-sandbox", "fixture-owner")
	marker, e := maintenance.Acquire(path, false)
	if e != nil {
		t.Fatal(e)
	}
	marker.Close()
	session, e := recovery.Open(ctx, path, "../../migrations", key, cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"}, fixturePolicy())
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	p := store.CubeRecoveryPlan{ID: "synthetic-journal", SandboxID: "fixture-sandbox", ExpectedRuntimeID: old.Guest.SandboxID, TargetTemplateID: template, TargetDomain: "cube.app", ExpectedConfigRevision: 1, ArtifactPaths: map[string]string{}, Artifacts: map[string]string{}}
	for _, role := range []string{"native_backup", "controller_backup", "workspace", "home", "history"} {
		p.ArtifactPaths[role] = filepath.Join(stage, role)
		p.Artifacts[role] = hashBytes([]byte(role))
	}
	if e = session.Begin(ctx, p); e != nil {
		t.Fatal(e)
	}
	j, e := session.Journal(ctx, p.ID)
	if e != nil || j.TaskCount != 1 || j.OwnerToken != "fixture-owner" {
		t.Fatal("frozen seeded identities missing", e)
	}
	if e = session.Commit(ctx, j.ID, old.Guest.SandboxID, 1); e == nil {
		t.Fatal("unverified binding committed")
	}
	if e = session.Fence(ctx, store.CubeRecoveryFence{RecoveryID: j.ID, OldRuntimeID: old.Guest.SandboxID, ArtifactsSHA256: j.ArtifactsSHA256, EvidenceSHA256: hashBytes([]byte("synthetic no-execution unit fixture")), OldExecutionStopped: true, ProviderRequestsDrained: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = session.TargetClient(ctx, j.ID, "http://127.0.0.1:1"); e == nil {
		t.Fatal("target client returned before provider creation")
	}
}
