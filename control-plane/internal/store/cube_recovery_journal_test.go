package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func recoveryFixture(t *testing.T, count int) (*Store, CubeRecoveryPlan) {
	t.Helper()
	ctx := context.Background()
	s, e := Open(ctx, "file:"+filepath.Join(t.TempDir(), "state.db")+"?_journal=WAL&_busy_timeout=5000&_fk=1", "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if s.db.Ping() == nil {
			s.Close()
		}
	})
	newAdmissionClient(t, s, "http://127.0.0.1:1", count)
	for i := 0; i < count; i++ {
		app := fmt.Sprintf("app-%d", i)
		id := fmt.Sprintf("stable-%d", i)
		old := fmt.Sprintf("old-%d", i)
		if e := s.CreateApp(ctx, &App{ID: app, OwnerToken: "private-owner", Name: "fixture"}); e != nil {
			t.Fatal(e)
		}
		lease, e := s.AdmissionBegin(ctx, "app:"+app, "", "tpl-reviewed", "create", fmt.Sprintf("lease-%d", i))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.AdmissionFinish(ctx, lease, old, "active"); e != nil {
			t.Fatal(e)
		}
		if e = s.Create(ctx, &Sandbox{ID: id, AppID: sql.NullString{String: app, Valid: true}, Status: "running", RuntimeProvider: "cube", RuntimeBinding: &RuntimeBinding{Provider: "cube", RuntimeID: old, TemplateID: "tpl-reviewed", Domain: "cube.test", TokenCiphertext: []byte("old-secret-encrypted"), TokenNonce: []byte("old-nonce")}}); e != nil {
			t.Fatal(e)
		}
	}
	p := CubeRecoveryPlan{ID: "recover-fixture", SandboxID: "stable-0", ExpectedRuntimeID: "old-0", TargetTemplateID: "tpl-reviewed", TargetDomain: "cube.test", SupervisorSHA256: recoveryHash("supervisor"), PlannedCiphertext: []byte("planned-encrypted"), PlannedNonce: []byte("planned-nonce"), Artifacts: map[string]string{}, ArtifactPaths: map[string]string{}}
	for _, role := range []string{"native_backup", "controller_backup", "workspace", "home"} {
		p.Artifacts[role] = recoveryHash(role)
		p.ArtifactPaths[role] = "/private/recovery/" + role
	}
	return s, p
}
func recoveryJournal(t *testing.T, s *Store) *CubeRecoveryJournal {
	t.Helper()
	j, e := s.GetCubeRecovery(context.Background(), "recover-fixture")
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func fenceRecovery(t *testing.T, s *Store) {
	t.Helper()
	j := recoveryJournal(t, s)
	if e := s.RecordCubeRecoveryFence(context.Background(), CubeRecoveryFence{RecoveryID: j.ID, OldRuntimeID: j.Old.RuntimeID, ArtifactsSHA256: j.ArtifactsSHA256, EvidenceSHA256: recoveryHash("operator-independent-stop-proof"), OldExecutionStopped: true, ProviderRequestsDrained: true}); e != nil {
		t.Fatal(e)
	}
}
func observedRecovery(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	j := recoveryJournal(t, s)
	_, e := s.CubeRecoveryCreateIntent(ctx, j.ID, "replacement-operation", recoveryHash("request"), j.SupervisorSHA256, j.Target.TemplateID, j.SandboxID, j.AppID)
	if e != nil {
		t.Fatal(e)
	}
	remote := &cube.Sandbox{SandboxID: "replacement", TemplateID: j.Target.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_id": j.SandboxID, "sandboxd_app_id": j.AppID, "sandboxd_recovery_id": j.ID, "sandboxd_admission_operation": "replacement-operation"}}
	if e = s.CubeRecoveryCreateObserved(ctx, j.ID, "replacement-operation", remote); e != nil {
		t.Fatal(e)
	}
	if e = s.CubeRecoveryCreateObserved(ctx, j.ID, "replacement-operation", remote); e != nil {
		t.Fatalf("ack recovery not idempotent: %v", e)
	}
	if e = s.RecordCubeRecoveryCredential(ctx, j.ID, remote.SandboxID, []byte("new-encrypted"), []byte("new-nonce")); e != nil {
		t.Fatal(e)
	}
}
func recoveryReceipt(t *testing.T, s *Store) CubeRecoveryVerification {
	t.Helper()
	j := recoveryJournal(t, s)
	a, e := s.AdmissionLookup(context.Background(), j.Target.RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	history := j.TaskFingerprint
	if j.TaskCount > 0 {
		history = j.Artifacts["history"]
	}
	return CubeRecoveryVerification{AdmissionToken: a.Token, RecoveryID: j.ID, SandboxID: j.SandboxID, AppID: j.AppID, OldRuntimeID: j.Old.RuntimeID, NewRuntimeID: j.Target.RuntimeID, TemplateID: j.Target.TemplateID, ArtifactsSHA256: j.ArtifactsSHA256, ConfigFingerprint: j.ConfigFingerprint, TaskFingerprint: j.TaskFingerprint, CredentialSHA256: CubeRecoveryCredentialSHA(j.Target.TokenCiphertext, j.Target.TokenNonce), EvidenceSHA256: recoveryHash("independent-authenticated-import-readiness-evidence"), WorkspaceSHA256: j.Artifacts["workspace"], HomeSHA256: j.Artifacts["home"], HistorySHA256: history, ConfigRevision: j.Old.ConfigRevision, Authenticated: true, WorkspaceVerified: true, HomeVerified: true, HistoryVerified: true, ConfigApplied: true, ApplicationReady: true}
}
func assertRecoveryCharge(t *testing.T, s *Store, want int) {
	t.Helper()
	var n int
	if e := s.db.QueryRow(`SELECT SUM(charged) FROM cube_admission`).Scan(&n); e != nil || n != want {
		t.Fatalf("charged=%d want=%d: %v", n, want, e)
	}
}

func TestCubeRecoveryJournalFullCapacityCAS(t *testing.T) {
	s, p := recoveryFixture(t, 4)
	ctx := context.Background()
	if e := s.BeginCubeRecovery(ctx, p); e != nil {
		t.Fatal(e)
	}
	assertRecoveryCharge(t, s, 4)
	if _, e := s.AdmissionLookup(ctx, "old-0"); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		t.Fatalf("old generation not quarantined: %v", e)
	}
	if _, e := s.AdmissionBegin(ctx, "app:app-0", "old-0", "tpl-reviewed", "connect", "stale"); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		t.Fatal(e)
	}
	if e := s.CommitCubeRecovery(ctx, p.ID, p.ExpectedRuntimeID, 0); e == nil {
		t.Fatal("unverified commit")
	}
	fenceRecovery(t, s)
	observedRecovery(t, s)
	assertRecoveryCharge(t, s, 4)
	if _, e := s.AdmissionBegin(ctx, "app:fifth", "", "tpl-reviewed", "create", "fifth"); !errors.Is(e, cube.ErrCapacityUnavailable) {
		t.Fatalf("fifth slot admitted: %v", e)
	}
	if _, e := s.GetRuntimeBinding(ctx, p.SandboxID); e != nil {
		t.Fatal(e)
	}
	v := recoveryReceipt(t, s)
	if e := s.VerifyCubeRecovery(ctx, v); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.CommitCubeRecovery(ctx, p.ID, p.ExpectedRuntimeID, 0) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	b, e := s.GetRuntimeBinding(ctx, p.SandboxID)
	if e != nil || b.RuntimeID != "replacement" || string(b.TokenCiphertext) != "new-encrypted" {
		t.Fatalf("binding commit failed: %v", e)
	}
	sb, e := s.Get(ctx, p.SandboxID)
	if e != nil || sb.AppID.String != "app-0" || sb.RuntimeProvider != "cube" {
		t.Fatal("stable app identity changed")
	}
	j := recoveryJournal(t, s)
	if string(j.Old.TokenCiphertext) != "old-secret-encrypted" || j.Phase != "complete" {
		t.Fatal("lost old recovery binding")
	}
	raw, _ := json.Marshal(j)
	for _, private := range []string{"private-owner", "old-secret", "new-encrypted", "/private/recovery", "planned-encrypted"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private journal data exposed")
		}
	}
	incomplete, e := s.HasIncompleteCubeRecoveries(ctx)
	if e != nil || incomplete {
		t.Fatal("completed journal blocks startup")
	}
	assertRecoveryCharge(t, s, 4)
	if _, e = s.AdmissionLookup(ctx, "old-0"); !errors.Is(e, cube.ErrRuntimeUnavailable) {
		t.Fatal("old quarantine cleared")
	}
}
func TestCubeRecoveryRequiresCompleteHistoryAndStableConfig(t *testing.T) {
	for _, change := range []string{"running-task", "missing-history", "config-drift", "config-content-drift", "task-result-drift", "missing-proof", "wrong-credential", "wrong-history", "changed-lease"} {
		t.Run(change, func(t *testing.T) {
			s, p := recoveryFixture(t, 1)
			ctx := context.Background()
			if change == "running-task" || change == "missing-history" || change == "task-result-drift" {
				if e := s.CreateTask(ctx, &Task{TaskID: "task-fixture", SandboxID: p.SandboxID, Agent: "fixture"}); e != nil {
					t.Fatal(e)
				}
				if change != "running-task" {
					if e := s.FinishTask(ctx, "task-fixture", "succeeded", `{"result":"original"}`); e != nil {
						t.Fatal(e)
					}
				}
				if change == "task-result-drift" {
					p.Artifacts["history"] = recoveryHash("history")
					p.ArtifactPaths["history"] = "/private/recovery/history"
				}
			}
			e := s.BeginCubeRecovery(ctx, p)
			if change == "running-task" || change == "missing-history" {
				if e == nil {
					t.Fatal("incomplete plan accepted")
				}
				var n int
				s.db.QueryRow(`SELECT COUNT(*) FROM cube_runtime_quarantine`).Scan(&n)
				if n != 0 {
					t.Fatal("failed plan left quarantine")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			fenceRecovery(t, s)
			observedRecovery(t, s)
			v := recoveryReceipt(t, s)
			switch change {
			case "missing-proof":
				v.HomeVerified = false
			case "wrong-credential":
				v.CredentialSHA256 = recoveryHash("wrong")
			case "wrong-history":
				v.HistorySHA256 = recoveryHash("wrong")
			}
			if change == "missing-proof" || change == "wrong-credential" || change == "wrong-history" {
				if e = s.VerifyCubeRecovery(ctx, v); e == nil {
					t.Fatal("invalid proof accepted")
				}
				return
			}
			if e = s.VerifyCubeRecovery(ctx, v); e != nil {
				t.Fatal(e)
			}
			switch change {
			case "config-drift":
				_, e = s.db.Exec(`UPDATE runtime_binding SET config_revision=config_revision+1 WHERE sandbox_id=?`, p.SandboxID)
			case "config-content-drift":
				e = s.CreateAppConfig(ctx, &AppConfig{ID: "new-config", AppID: "app-0", Key: "MODE", ValuePlaintext: sql.NullString{String: "changed", Valid: true}, AccessPolicy: "control_plane_only"})
			case "task-result-drift":
				e = s.FinishTask(ctx, "task-fixture", "succeeded", `{"result":"changed"}`)
			case "changed-lease":
				_, e = s.db.Exec(`UPDATE cube_admission SET token='newer-lease' WHERE runtime_id='replacement'`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = s.CommitCubeRecovery(ctx, p.ID, p.ExpectedRuntimeID, 0); e == nil {
				t.Fatal("stale verification committed")
			}
			b, e := s.GetRuntimeBinding(ctx, p.SandboxID)
			if e != nil || b.RuntimeID != p.ExpectedRuntimeID {
				t.Fatal("failed CAS lost old binding")
			}
			assertRecoveryCharge(t, s, 1)
		})
	}
}
func TestCubeRecoveryAmbiguousCreateSurvivesReopenAndExactAdoption(t *testing.T) {
	s, p := recoveryFixture(t, 1)
	ctx := context.Background()
	supervisor := "fixture-supervisor"
	p.SupervisorSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(supervisor)))
	var mu sync.Mutex
	var remote cube.Sandbox
	var posts atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" {
			posts.Add(1)
			var in cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&in)
			remote = cube.Sandbox{SandboxID: "replacement", TemplateID: in.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: in.Metadata}
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(remote)
	}))
	defer provider.Close()
	client := newAdmissionClient(t, s, provider.URL, 1)
	if e := s.BeginCubeRecovery(ctx, p); e != nil {
		t.Fatal(e)
	}
	fenceRecovery(t, s)
	in := admissionInput(p.SandboxID)
	in.Metadata["sandboxd_app_id"] = "app-0"
	in.EnvVars = map[string]string{"RUNTIMED_HTTP_TOKEN": supervisor}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := client.CreateRecovery(cancelled, s, p.ID, in); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if posts.Load() != 0 {
		t.Fatal("cancelled request reached provider")
	}
	if _, e := client.CreateRecovery(ctx, s, p.ID, in); !errors.Is(e, cube.ErrAdmissionPending) {
		t.Fatal(e)
	}
	if posts.Load() != 1 {
		t.Fatal("unexpected POST count")
	}
	j := recoveryJournal(t, s)
	if j.Phase != "creating" {
		t.Fatal("ambiguous creation forgotten")
	}
	assertRecoveryCharge(t, s, 1)
	var path string
	if e := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, "file:"+path+"?_journal=WAL&_busy_timeout=5000&_fk=1", "../../migrations")
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	s = reopened
	client = newAdmissionClient(t, s, provider.URL, 1)
	if _, e = client.CreateRecovery(ctx, s, p.ID, in); !errors.Is(e, cube.ErrAdmissionPending) {
		t.Fatal(e)
	}
	if posts.Load() != 1 {
		t.Fatal("repeated ambiguous POST")
	}
	lease, e := s.AdmissionLookup(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AdmissionFinish(ctx, lease, "replacement", "active"); !errors.Is(e, cube.ErrAdmissionPending) {
		t.Fatalf("ordinary adoption bypassed recovery: %v", e)
	}
	mu.Lock()
	remote.Metadata["sandboxd_id"] = "wrong-owner"
	mu.Unlock()
	if _, e = client.AdoptRecovery(ctx, s, p.ID, "replacement"); e == nil {
		t.Fatal("wrong identity adopted")
	}
	mu.Lock()
	remote.Metadata["sandboxd_id"] = p.SandboxID
	mu.Unlock()
	if _, e = client.AdoptRecovery(ctx, s, p.ID, "replacement"); e != nil {
		t.Fatal(e)
	}
	if _, e = client.AdoptRecovery(ctx, s, p.ID, "replacement"); e != nil {
		t.Fatal("ack-before-credential crash cannot recover", e)
	}
	assertRecoveryCharge(t, s, 1)
	b, e := s.GetRuntimeBinding(ctx, p.SandboxID)
	if e != nil || b.RuntimeID != p.ExpectedRuntimeID {
		t.Fatal("adoption switched binding prematurely")
	}
}

func TestCubeRecoveryCallerCancellationAfterIntentCompletesAcknowledgment(t *testing.T) {
	s, p := recoveryFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := "fixture-supervisor"
	p.SupervisorSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(supervisor)))
	var remote cube.Sandbox
	var posts atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			var in cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&in)
			remote = cube.Sandbox{SandboxID: "replacement", TemplateID: in.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: in.Metadata}
			cancel()
			w.WriteHeader(http.StatusCreated)
		}
		json.NewEncoder(w).Encode(remote)
	}))
	defer provider.Close()
	client := newAdmissionClient(t, s, provider.URL, 1)
	if e := s.BeginCubeRecovery(ctx, p); e != nil {
		t.Fatal(e)
	}
	fenceRecovery(t, s)
	in := admissionInput(p.SandboxID)
	in.Metadata["sandboxd_app_id"] = "app-0"
	in.EnvVars = map[string]string{"RUNTIMED_HTTP_TOKEN": supervisor}
	if _, e := client.CreateRecovery(ctx, s, p.ID, in); e != nil {
		t.Fatal(e)
	}
	if posts.Load() != 1 {
		t.Fatal("unexpected provider mutation count")
	}
	if j := recoveryJournal(t, s); j.Phase != "created" {
		t.Fatalf("caller cancellation stranded acknowledgment: %s", j.Phase)
	}
}

func TestCubeRecoveryQuarantineRejectsStaleObservationAndGeneration(t *testing.T) {
	s, p := recoveryFixture(t, 1)
	ctx := context.Background()
	old, e := s.AdmissionLookup(ctx, "old-0")
	if e != nil {
		t.Fatal(e)
	}
	wrong := p
	wrong.ExpectedRuntimeID = "different-generation"
	if e = s.BeginCubeRecovery(ctx, wrong); !errors.Is(e, ErrConflict) {
		t.Fatal("wrong generation accepted", e)
	}
	if e = s.BeginCubeRecovery(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e = s.AdmissionObserveReleased(ctx, old, true); e != nil {
		t.Fatal(e)
	}
	assertRecoveryCharge(t, s, 1)
	fenceRecovery(t, s)
	observedRecovery(t, s)
	if e = s.AdmissionObserveReleased(ctx, old, false); e != nil {
		t.Fatal(e)
	}
	assertRecoveryCharge(t, s, 1)
	if e = s.AdmissionFinish(ctx, old, "old-0", "released"); !errors.Is(e, cube.ErrAdmissionPending) {
		t.Fatal("stale lease changed reservation", e)
	}
	a, e := s.AdmissionLookup(ctx, "replacement")
	if e != nil || a.State != "active" {
		t.Fatal("old observation changed replacement", e)
	}
}

func TestCubeRecoveryPinsRetainedReviewedDomain(t *testing.T) {
	s, p := recoveryFixture(t, 1)
	ctx := context.Background()
	wrong := p
	wrong.TargetDomain = "other.example"
	if e := s.BeginCubeRecovery(ctx, wrong); !errors.Is(e, ErrConflict) {
		t.Fatalf("unreviewed domain accepted: %v", e)
	}
	if e := s.BeginCubeRecovery(ctx, p); e != nil {
		t.Fatal(e)
	}
	fenceRecovery(t, s)
	j := recoveryJournal(t, s)
	_, e := s.CubeRecoveryCreateIntent(ctx, j.ID, "replacement-operation", recoveryHash("request"), j.SupervisorSHA256, j.Target.TemplateID, j.SandboxID, j.AppID)
	if e != nil {
		t.Fatal(e)
	}
	remote := &cube.Sandbox{SandboxID: "replacement", Domain: "other.example", TemplateID: j.Target.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_id": j.SandboxID, "sandboxd_app_id": j.AppID, "sandboxd_recovery_id": j.ID, "sandboxd_admission_operation": "replacement-operation"}}
	if e = s.CubeRecoveryCreateObserved(ctx, j.ID, "replacement-operation", remote); !errors.Is(e, ErrConflict) {
		t.Fatalf("provider domain mismatch accepted: %v", e)
	}
	assertRecoveryCharge(t, s, 1)
	if recoveryJournal(t, s).Phase != "creating" {
		t.Fatal("invalid provider response changed journal")
	}
	remote.Domain = p.TargetDomain
	if e = s.CubeRecoveryCreateObserved(ctx, j.ID, "replacement-operation", remote); e != nil {
		t.Fatal(e)
	}
}
