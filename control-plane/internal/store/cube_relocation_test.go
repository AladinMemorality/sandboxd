package store

import (
	"context"
	"errors"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func relocationFixture(t *testing.T) (*Store, cube.AdmissionStore) {
	t.Helper()
	s, _ := recoveryFixture(t, 1)
	ctx := context.Background()
	a, e := s.AdmissionLookup(ctx, "old-0")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AdmissionObserveReleased(ctx, a, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE sandbox SET status='stopped' WHERE id='stable-0'`); e != nil {
		t.Fatal(e)
	}
	w, e := s.AdmissionPartition("b200-01")
	if e != nil {
		t.Fatal(e)
	}
	if e = w.AdmissionPolicy(ctx, 4, "cpu=2;memory_mb=2048"); e != nil {
		t.Fatal(e)
	}
	if e = w.(*workerAdmission).ConfigureNodeIdentity(ctx, "10.254.240.2"); e != nil {
		t.Fatal(e)
	}
	return s, w
}

func targetRelocation(t *testing.T, w cube.AdmissionStore, j CubeRelocation) RuntimeBinding {
	t.Helper()
	ctx := context.Background()
	a, e := w.AdmissionBegin(ctx, j.TargetKey, "", j.TemplateID, "create", "new-token")
	if e != nil {
		t.Fatal(e)
	}
	if e = w.AdmissionFinish(ctx, a, "new-runtime", "active"); e != nil {
		t.Fatal(e)
	}
	return RuntimeBinding{SandboxID: j.SandboxID, Provider: "cube", RuntimeID: "new-runtime", TemplateID: j.TemplateID, Domain: j.Domain, ConfigRevision: j.ConfigRevision, ConfigAppliedRevision: j.ConfigRevision, TokenCiphertext: []byte("encrypted-new"), TokenNonce: []byte("nonce-new")}
}

func TestCubeRelocationPreservesIdentityAndMovesChargedReservationAtomically(t *testing.T) {
	s, w := relocationFixture(t)
	ctx := context.Background()
	j, e := s.BeginCubeRelocation(ctx, "move-1", "stable-0", "old-0", "b200-01")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdmissionLookup(ctx, "old-0"); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		t.Fatalf("source can still resume: %v", e)
	}
	if _, e = s.AdmissionBegin(ctx, "app:app-0", "old-0", "tpl-reviewed", "connect", "stale"); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		t.Fatalf("source connect not fenced: %v", e)
	}
	b := targetRelocation(t, w, j)
	// The currently deployed reconciler surfaces quarantine as an error while
	// retaining the stopped source and its binding. That must not strand a move.
	if _, e = s.db.Exec(`UPDATE sandbox SET status='error',error_message='Cube runtime requires operator recovery; binding retained' WHERE id='stable-0'`); e != nil {
		t.Fatal(e)
	}
	if e = s.CommitCubeRelocation(ctx, j.ID, "new-token", recoveryHash("verified workspace home history config readiness"), b); e != nil {
		t.Fatal(e)
	}
	bound, e := s.GetRuntimeBinding(ctx, "stable-0")
	if e != nil || bound.RuntimeID != "new-runtime" {
		t.Fatalf("binding: %+v %v", bound, e)
	}
	a, e := s.AdmissionLookupKey(ctx, "app:app-0")
	if e != nil || a.WorkerID != "b200-01" || a.Charged != 1 || a.RuntimeID != "new-runtime" {
		t.Fatalf("admission: %+v %v", a, e)
	}
	if _, e = s.AdmissionLookup(ctx, "old-0"); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		t.Fatal("retired source became accessible")
	}
	var apps, sandboxes int
	s.db.QueryRow(`SELECT count(*) FROM app`).Scan(&apps)
	s.db.QueryRow(`SELECT count(*) FROM sandbox`).Scan(&sandboxes)
	if apps != 1 || sandboxes != 1 {
		t.Fatal("move changed customer identities")
	}
	if e = s.AbortCubeRelocation(ctx, j.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("completed move can be accidentally rolled back")
	}
}

func TestCubeRelocationRejectsStaleConfigAndWrongTarget(t *testing.T) {
	for _, change := range []string{"config", "token", "worker", "credential", "quarantine"} {
		t.Run(change, func(t *testing.T) {
			s, w := relocationFixture(t)
			ctx := context.Background()
			j, e := s.BeginCubeRelocation(ctx, "move-1", "stable-0", "old-0", "b200-01")
			if e != nil {
				t.Fatal(e)
			}
			b := targetRelocation(t, w, j)
			token := "new-token"
			switch change {
			case "config":
				_, e = s.db.Exec(`UPDATE runtime_binding SET config_revision=config_revision+1`)
			case "token":
				token = "wrong"
			case "worker":
				_, e = s.db.Exec(`UPDATE cube_admission SET worker_id='vps' WHERE admission_key=?`, j.TargetKey)
			case "credential":
				_, e = s.db.Exec(`UPDATE runtime_binding SET token_ciphertext=x'1234'`)
			case "quarantine":
				_, e = s.db.Exec(`DELETE FROM cube_runtime_quarantine`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = s.CommitCubeRelocation(ctx, j.ID, token, recoveryHash("proof"), b); e == nil {
				t.Fatal("unsafe relocation accepted")
			}
			bound, _ := s.GetRuntimeBinding(ctx, j.SandboxID)
			if bound.RuntimeID != "old-0" {
				t.Fatal("failed commit changed source binding")
			}
		})
	}
}

func TestCubeRelocationRefusesUncertainSourceAndTarget(t *testing.T) {
	s, w := relocationFixture(t)
	ctx := context.Background()
	a, e := s.AdmissionBegin(ctx, "app:app-0", "old-0", "tpl-reviewed", "connect", "in-flight")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.BeginCubeRelocation(ctx, "move-1", "stable-0", "old-0", "b200-01"); e == nil {
		t.Fatal("pending source accepted")
	}
	if e = s.AdmissionFinish(ctx, a, "old-0", "released"); e != nil {
		t.Fatal(e)
	}
	j, e := s.BeginCubeRelocation(ctx, "move-1", "stable-0", "old-0", "b200-01")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.AdmissionBegin(ctx, j.TargetKey, "", j.TemplateID, "create", "uncertain"); e != nil {
		t.Fatal(e)
	}
	if e = s.AbortCubeRelocation(ctx, j.ID); !errors.Is(e, cube.ErrAdmissionPending) {
		t.Fatalf("uncertain target released source: %v", e)
	}
}

func TestCubeRelocationAbortRestoresUnchangedSource(t *testing.T) {
	s, _ := relocationFixture(t)
	ctx := context.Background()
	j, e := s.BeginCubeRelocation(ctx, "move-1", "stable-0", "old-0", "b200-01")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AbortCubeRelocation(ctx, j.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdmissionLookup(ctx, "old-0"); e != nil {
		t.Fatal(e)
	}
}

func TestCubeRelocationExplicitSmallerDestinationPreservesSourceFence(t *testing.T) {
	s, w := relocationFixture(t)
	ctx := context.Background()
	if _, err := s.BeginCubeRelocation(ctx, "move-small", "stable-0", "old-0", "b200-01", "small"); err == nil {
		t.Fatal("unenrolled profile accepted")
	}
	cfg := resourceTestConfig(12000)
	if _, err := s.db.Exec(`UPDATE cube_admission_policy SET profile='resource-budget-v1',max_active=? WHERE worker_id='b200-01'`, cfg.MaxActive); err != nil {
		t.Fatal(err)
	}
	if err := w.(*workerAdmission).ConfigureResourceBudget(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	j, err := s.BeginCubeRelocation(ctx, "move-small", "stable-0", "old-0", "b200-01", "small")
	if err != nil {
		t.Fatal(err)
	}
	if j.TemplateID != "tpl-reviewed" || j.DestinationTemplate() != "small" {
		t.Fatalf("source/target contract lost: %+v", j)
	}
	if _, err = s.BeginCubeRelocation(ctx, "move-small", "stable-0", "old-0", "b200-01", "large"); !errors.Is(err, ErrConflict) {
		t.Fatal("pending destination changed")
	}
	target := j
	target.TemplateID = j.DestinationTemplate()
	b := targetRelocation(t, w, target)
	wrong := b
	wrong.TemplateID = j.TemplateID
	if err = s.CommitCubeRelocation(ctx, j.ID, "new-token", recoveryHash("proof"), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("old template accepted as new target")
	}
	if err = s.CommitCubeRelocation(ctx, j.ID, "new-token", recoveryHash("proof"), b); err != nil {
		t.Fatal(err)
	}
	bound, err := s.GetRuntimeBinding(ctx, "stable-0")
	if err != nil || bound.TemplateID != "small" || bound.RuntimeID != "new-runtime" {
		t.Fatalf("destination binding: %+v %v", bound, err)
	}
	if _, err = s.AdmissionLookup(ctx, "old-0"); !errors.Is(err, cube.ErrRuntimeUnavailable) {
		t.Fatal("source became reachable")
	}
}

func TestCubeRelocationSameWorkerRequiresExplicitChangedRuntimeProfile(t *testing.T) {
	for _, destination := range []string{"", "tpl-reviewed", "unknown", "builder"} {
		t.Run(destination, func(t *testing.T) {
			s, _ := relocationFixture(t)
			if err := s.ConfigureNodeIdentity(context.Background(), "10.0.2.15"); err != nil {
				t.Fatal(err)
			}
			cfg := resourceTestConfig(12000)
			cfg.Templates["tpl-reviewed"] = cube.AdmissionResources{CPUCount: 2, MemoryMB: 2048}
			cfg.ResourceBudget.Profiles["tpl-reviewed"] = cube.ResourceProfile{CPUMillis: 500, WritableDiskMB: 8192, Kind: "runtime"}
			if _, err := s.db.Exec(`UPDATE cube_admission_policy SET profile='resource-budget-v1',max_active=? WHERE worker_id='vps'`, cfg.MaxActive); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureResourceBudget(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			var err error
			if destination == "" {
				_, err = s.BeginCubeRelocation(context.Background(), "reprofile", "stable-0", "old-0", "vps")
			} else {
				_, err = s.BeginCubeRelocation(context.Background(), "reprofile", "stable-0", "old-0", "vps", destination)
			}
			if err == nil {
				t.Fatal("unreviewed same-worker replacement accepted")
			}
			if _, err = s.AdmissionLookup(context.Background(), "old-0"); err != nil {
				t.Fatalf("rejected reprofile fenced source: %v", err)
			}
		})
	}
}

func TestCubeRelocationSameWorkerReprofileCommitAndAbort(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort", true: "commit"}[commit], func(t *testing.T) {
			s, _ := relocationFixture(t)
			ctx := context.Background()
			if err := s.ConfigureNodeIdentity(context.Background(), "10.0.2.15"); err != nil {
				t.Fatal(err)
			}
			cfg := resourceTestConfig(12000)
			cfg.Templates["tpl-reviewed"] = cube.AdmissionResources{CPUCount: 2, MemoryMB: 2048}
			cfg.ResourceBudget.Profiles["tpl-reviewed"] = cube.ResourceProfile{CPUMillis: 500, WritableDiskMB: 8192, Kind: "runtime"}
			if _, err := s.db.Exec(`UPDATE cube_admission_policy SET profile='resource-budget-v1',max_active=? WHERE worker_id='vps'`, cfg.MaxActive); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureResourceBudget(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			j, err := s.BeginCubeRelocation(ctx, "reprofile", "stable-0", "old-0", "vps", "large")
			if err != nil {
				t.Fatal(err)
			}
			if j.SourceAdmission.WorkerID != "vps" || j.TemplateID != "tpl-reviewed" {
				t.Fatal("source reservation lost")
			}
			if !commit {
				if err = s.AbortCubeRelocation(ctx, j.ID); err != nil {
					t.Fatal(err)
				}
				old, err := s.AdmissionLookup(ctx, "old-0")
				if err != nil || old != j.SourceAdmission {
					t.Fatalf("abort changed source: %+v %v", old, err)
				}
				return
			}
			target := j
			target.TemplateID = j.DestinationTemplate()
			b := targetRelocation(t, s, target)
			if err = s.CommitCubeRelocation(ctx, j.ID, "new-token", recoveryHash("verified current content"), b); err != nil {
				t.Fatal(err)
			}
			active, err := s.AdmissionLookupKey(ctx, "app:app-0")
			if err != nil || active.WorkerID != "vps" || active.TemplateID != "large" || active.RuntimeID != "new-runtime" || active.Charged != 1 {
				t.Fatalf("destination reservation: %+v %v", active, err)
			}
			retired, err := s.AdmissionLookupKey(ctx, "relocation-retired:"+j.ID)
			if err != nil || retired.RuntimeID != "old-0" || retired.Charged != 0 {
				t.Fatalf("retired reservation: %+v %v", retired, err)
			}
			if _, err = s.AdmissionLookup(ctx, "old-0"); !errors.Is(err, cube.ErrRuntimeUnavailable) {
				t.Fatal("source can resume after replacement")
			}
			binding, err := s.GetRuntimeBinding(ctx, "stable-0")
			if err != nil || binding.RuntimeID != "new-runtime" || binding.TemplateID != "large" {
				t.Fatalf("binding: %+v %v", binding, err)
			}
		})
	}
}

func TestRetainedRelocationSourcesRequiresCompletedUnchangedFencedSource(t *testing.T) {
	for _, change := range []string{"none", "charged", "token", "quarantine", "bound"} {
		t.Run(change, func(t *testing.T) {
			s, w := relocationFixture(t)
			ctx := context.Background()
			j, err := s.BeginCubeRelocation(ctx, "retain-1", "stable-0", "old-0", "b200-01")
			if err != nil {
				t.Fatal(err)
			}
			ids, err := RetainedRelocationSources(ctx, s.db, "vps")
			if err != nil || len(ids) != 0 {
				t.Fatalf("unfinished relocation retained: %v %v", ids, err)
			}
			b := targetRelocation(t, w, j)
			if err = s.CommitCubeRelocation(ctx, j.ID, "new-token", recoveryHash("content proof"), b); err != nil {
				t.Fatal(err)
			}
			ids, err = RetainedRelocationSources(ctx, s.db, "b200-01")
			if err != nil || len(ids) != 0 {
				t.Fatalf("other worker source retained: %v %v", ids, err)
			}
			switch change {
			case "charged":
				_, err = s.db.Exec(`UPDATE cube_admission SET charged=1 WHERE runtime_id='old-0'`)
			case "token":
				_, err = s.db.Exec(`UPDATE cube_admission SET token='different' WHERE runtime_id='old-0'`)
			case "quarantine":
				_, err = s.db.Exec(`DELETE FROM cube_runtime_quarantine WHERE runtime_id='old-0'`)
			case "bound":
				_, err = s.db.Exec(`UPDATE runtime_binding SET runtime_id='old-0' WHERE sandbox_id='stable-0'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			ids, err = RetainedRelocationSources(ctx, s.db, "vps")
			if change == "none" {
				if err != nil || len(ids) != 1 || ids[0] != "old-0" {
					t.Fatalf("verified source not retained: %v %v", ids, err)
				}
			} else if err == nil {
				t.Fatalf("changed retained source accepted: %s", change)
			}
		})
	}
}

func TestCubeSameProfileReplacementRetainsFencingAndReservation(t *testing.T) {
	for _, action := range []string{"commit", "abort", "wrong-worker", "wrong-profile", "running", "pending", "replay-mode"} {
		t.Run(action, func(t *testing.T) {
			s, _ := relocationFixture(t)
			ctx := context.Background()
			if err := s.ConfigureNodeIdentity(ctx, "10.0.2.15"); err != nil {
				t.Fatal(err)
			}
			cfg := resourceTestConfig(12000)
			cfg.Templates["tpl-reviewed"] = cube.AdmissionResources{CPUCount: 2, MemoryMB: 2048}
			cfg.ResourceBudget.Profiles["tpl-reviewed"] = cube.ResourceProfile{CPUMillis: 500, WritableDiskMB: 8192, Kind: "runtime"}
			if _, err := s.db.Exec(`UPDATE cube_admission_policy SET profile='resource-budget-v1',max_active=? WHERE worker_id='vps'`, cfg.MaxActive); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureResourceBudget(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			worker, template := "vps", "tpl-reviewed"
			switch action {
			case "wrong-worker":
				worker = "b200-01"
			case "wrong-profile":
				template = "large"
			case "running":
				if _, err := s.db.Exec(`UPDATE sandbox SET status='running' WHERE id='stable-0'`); err != nil {
					t.Fatal(err)
				}
			case "pending":
				if _, err := s.db.Exec(`UPDATE cube_admission SET state='pending',charged=1 WHERE runtime_id='old-0'`); err != nil {
					t.Fatal(err)
				}
			}
			j, err := s.BeginCubeSameProfileReplacement(ctx, "replace", "stable-0", "old-0", worker, template)
			if action == "wrong-worker" || action == "wrong-profile" || action == "running" || action == "pending" {
				if err == nil {
					t.Fatal("invalid replacement accepted")
				}
				bound, e := s.GetRuntimeBinding(ctx, "stable-0")
				if e != nil || bound.RuntimeID != "old-0" {
					t.Fatal("refusal changed source binding")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !j.SameProfileReplacement || j.DestinationTemplate() != "tpl-reviewed" {
				t.Fatal("replacement intent lost")
			}
			if _, e := s.AdmissionLookup(ctx, "old-0"); !errors.Is(e, cube.ErrRuntimeUnavailable) {
				t.Fatal("source was not fenced")
			}
			if action == "replay-mode" {
				if _, e := s.BeginCubeRelocation(ctx, "replace", "stable-0", "old-0", "vps", "tpl-reviewed"); !errors.Is(e, ErrConflict) {
					t.Fatal("replacement replayed as ordinary relocation")
				}
				if _, e := s.BeginCubeSameProfileReplacement(ctx, "replace", "stable-0", "old-0", "vps", "tpl-reviewed"); e != nil {
					t.Fatal(e)
				}
				return
			}
			if action == "abort" {
				if err := s.AbortCubeRelocation(ctx, j.ID); err != nil {
					t.Fatal(err)
				}
				old, err := s.AdmissionLookup(ctx, "old-0")
				if err != nil || old != j.SourceAdmission {
					t.Fatal("abort changed source reservation")
				}
				return
			}
			target := targetRelocation(t, s, j)
			if err := s.CommitCubeRelocation(ctx, j.ID, "wrong-token", recoveryHash("verified"), target); !errors.Is(err, cube.ErrAdmissionPending) {
				t.Fatalf("wrong target token accepted: %v", err)
			}
			if err := s.CommitCubeRelocation(ctx, j.ID, "new-token", recoveryHash("verified source home history config"), target); err != nil {
				t.Fatal(err)
			}
			bound, err := s.GetRuntimeBinding(ctx, "stable-0")
			if err != nil || bound.RuntimeID != "new-runtime" || bound.TemplateID != "tpl-reviewed" {
				t.Fatal("replacement changed profile or lost binding")
			}
			retained, err := s.AdmissionLookupKey(ctx, "relocation-retired:"+j.ID)
			if err != nil || retained.RuntimeID != "old-0" || retained.Charged != 0 {
				t.Fatal("source reservation was not retained")
			}
		})
	}
}
