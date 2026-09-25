package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/recovery"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

func TestRetainedSourceBranchUsesTypedProviderStateAndRetainsBindingCharge(t *testing.T) {
	for _, state := range []string{"unknown", "stopped"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			old, _ := ownedEvidence()
			old.Supervisor = "synthetic"
			old.Guest.TrafficAccessToken = "synthetic-traffic"
			stage := t.TempDir()
			path := filepath.Join(stage, "fixture.db")
			key, err := secrets.Load("", filepath.Join(stage, "key"))
			if err != nil {
				t.Fatal(err)
			}
			seedDatabase(ctx, path, "../../migrations", key, old, "fixture-app", "fixture-sandbox", "fixture-owner")
			db := openFixture(ctx, path, "../../migrations")
			defer db.Close()
			before, err := db.GetRuntimeBinding(ctx, "fixture-sandbox")
			if err != nil {
				t.Fatal(err)
			}
			admissionBefore, err := db.AdmissionLookup(ctx, old.Guest.SandboxID)
			if err != nil {
				t.Fatal(err)
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/sandboxes/"+old.Guest.SandboxID {
					t.Error("unexpected provider mutation")
					w.WriteHeader(400)
					return
				}
				_ = json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: old.Guest.SandboxID, TemplateID: template, State: state, CPUCount: 2, MemoryMB: 2048})
			}))
			defer provider.Close()
			client, err := cube.New(cube.Config{APIURL: provider.URL, APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if err = client.ConfigureAdmission(ctx, db, fixturePolicy()); err != nil {
				t.Fatal(err)
			}
			_, runtimeErr := client.Get(ctx, old.Guest.SandboxID)
			if !errors.Is(runtimeErr, cube.ErrRuntimeUnavailable) {
				t.Fatal("real typed state missing", runtimeErr)
			}
			inventory := "NODES_SCANNED 1/1\nSANDBOX_COUNT 1\n" + old.Guest.SandboxID + " unknown rest\n"
			branch, err := oldStateBranch(runtimeErr, inventory, old.Guest.SandboxID)
			if err != nil || branch != "unavailable-retained" {
				t.Fatal("retained owned branch rejected", err)
			}
			for _, bad := range []string{strings.Replace(inventory, old.Guest.SandboxID, strings.Repeat("f", 32), 1), strings.Replace(inventory, "1/1", "0/1", 1), strings.Replace(inventory, "COUNT 1", "COUNT 2", 1), "NODES_SCANNED 1/1\nSANDBOX_COUNT 0\n"} {
				if _, err = oldStateBranch(runtimeErr, bad, old.Guest.SandboxID); err == nil {
					t.Fatal("retained state accepted incomplete or foreign inventory")
				}
			}
			after, err := db.GetRuntimeBinding(ctx, "fixture-sandbox")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("original canonical binding modified", err)
			}
			admissionAfter, err := db.AdmissionLookup(ctx, old.Guest.SandboxID)
			if err != nil || admissionAfter != admissionBefore || admissionAfter.Charged != 1 {
				t.Fatal("retained source admission released", err)
			}
		})
	}
}

func TestRetainedEvidenceCannotSubstituteMissingProviderPurpose(t *testing.T) {
	old, _ := ownedEvidence()
	old.BootID = "before"
	proof := missingEvidence{Purpose: "OWNED_CURRENT_DISK_RETAINED_PROVIDER", OldID: old.Guest.SandboxID, Fixture: old.Fixture, MachineID: old.WorkerMachineID, PreviousBoot: "before", CurrentBoot: "after", ArchiveSHA: strings.Repeat("d", 64), NoTask: true, NoVMM: true, Fenced: true, Checked: 995, Expires: 1100, Files: map[string]string{}}
	for _, name := range []string{"cubebox.json", "storage.json", "plan.json", "rescue-input.json", "fence.json", "export-report.json"} {
		proof.Files[name] = strings.Repeat("e", 64)
	}
	if e := validateSourceReceipt(proof, old, proof.ArchiveSHA, "after", 1000, "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); e != nil {
		t.Fatal(e)
	}
	if e := validateMissingReceipt(proof, old, proof.ArchiveSHA, "after", 1000); e == nil {
		t.Fatal("retained receipt authorized missing-provider branch")
	}
	proof.Purpose = "OWNED_CURRENT_DISK_MISSING_PROVIDER"
	if e := validateSourceReceipt(proof, old, proof.ArchiveSHA, "after", 1000, "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); e == nil {
		t.Fatal("historical404 receipt authorized retained source")
	}
	proof.Purpose = "OWNED_CURRENT_DISK_RETAINED_PROVIDER"
	proof.NoVMM = false
	if e := validateSourceReceipt(proof, old, proof.ArchiveSHA, "after", 1000, "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); e == nil {
		t.Fatal("unfenced source accepted")
	}
}

func TestPostCaptureRebootRequiresUnchangedDiskAndExactReceiptChain(t *testing.T) {
	old, _ := ownedEvidence()
	old.BootID = "original"
	old.DataUUID = "reviewed-data"
	files := map[string]string{"rescue-input.json": "manifest-sha", "fence.json": "fence-sha", "plan.json": "plan-sha"}
	good := postCaptureReboot{Purpose: "CUBE_CAPTURE_POST_REBOOT_CONTINUITY", ID: old.Guest.SandboxID, Machine: old.WorkerMachineID, DataUUID: old.DataUUID, CaptureBoot: "captured", ExecutionBoot: "current", CapturedDisk: "latest-disk", CurrentDisk: "latest-disk", Manifest: "manifest-sha", Fence: "fence-sha", Plan: "plan-sha", Identity: true, Orderly: true, NoTask: true, NoVMM: true, Drained: true}
	if e := validatePostCaptureReboot(good, old, "captured", "current", "latest-disk", files); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*postCaptureReboot){
		func(p *postCaptureReboot) { p.ID = "other" }, func(p *postCaptureReboot) { p.Machine = "other" }, func(p *postCaptureReboot) { p.DataUUID = "other" },
		func(p *postCaptureReboot) { p.CaptureBoot = "wrong" }, func(p *postCaptureReboot) { p.ExecutionBoot = "captured" }, func(p *postCaptureReboot) { p.ExecutionBoot = "original" },
		func(p *postCaptureReboot) { p.CurrentDisk = "newer-unbound-disk" }, func(p *postCaptureReboot) { p.CapturedDisk = "old-checkpoint" }, func(p *postCaptureReboot) { p.Manifest = "other" },
		func(p *postCaptureReboot) { p.Fence = "other" }, func(p *postCaptureReboot) { p.Plan = "other" }, func(p *postCaptureReboot) { p.Identity = false }, func(p *postCaptureReboot) { p.Orderly = false },
		func(p *postCaptureReboot) { p.NoTask = false }, func(p *postCaptureReboot) { p.NoVMM = false }, func(p *postCaptureReboot) { p.Drained = false },
	} {
		bad := good
		mutate(&bad)
		if e := validatePostCaptureReboot(bad, old, "captured", "current", "latest-disk", files); e == nil {
			t.Fatal("invalid reboot chain accepted")
		}
	}
}

func TestExplicitRepairEvidenceRequiresCleanCloneAndHashBoundLogs(t *testing.T) {
	files := map[string]string{"repair-receipt.json": strings.Repeat("a", 64), "repair-fsck.log": strings.Repeat("b", 64), "verify-fsck.log": strings.Repeat("c", 64)}
	source, clone := strings.Repeat("d", 64), strings.Repeat("e", 64)
	good := map[string]any{"purpose": "CUBE_CURRENT_DISK_EXPLICIT_CLONE_REPAIR", "source_sha256": source, "clone_before_sha256": source, "clone_after_sha256": clone, "clean_verified": true, "steps": []map[string]any{{"option": "-fy", "returncode": 1, "log": "repair-fsck.log", "log_sha256": files["repair-fsck.log"]}, {"option": "-fn", "returncode": 0, "log": "verify-fsck.log", "log_sha256": files["verify-fsck.log"]}}}
	check := func(value map[string]any) error {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return validateRepairEvidence(files["repair-receipt.json"], source, clone, files, map[string]json.RawMessage{"repair-receipt.json": b})
	}
	if err := check(good); err != nil {
		t.Fatal(err)
	}
	for key, bad := range map[string]any{"purpose": "normal", "source_sha256": clone, "clone_before_sha256": clone, "clone_after_sha256": source, "clean_verified": false, "steps": []map[string]any{{"option": "-fy", "returncode": 1, "log": "repair-fsck.log", "log_sha256": files["repair-fsck.log"]}, {"option": "-fn", "returncode": 1, "log": "verify-fsck.log", "log_sha256": files["verify-fsck.log"]}}} {
		old := good[key]
		good[key] = bad
		if err := check(good); err == nil {
			t.Errorf("accepted %s", key)
		}
		good[key] = old
	}
	if err := validateRepairEvidence("", source, "", files, nil); err == nil {
		t.Fatal("ignored undeclared repair")
	}
	delete(files, "verify-fsck.log")
	if err := check(good); err == nil {
		t.Fatal("accepted unbound verification log")
	}
}
