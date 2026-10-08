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
