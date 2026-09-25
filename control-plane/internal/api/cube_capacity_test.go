package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/activity"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func capacityFixture(t *testing.T, mode string) (*Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	s, appID := newConfigTestServer(t)
	s.Locks = idlock.New()
	s.Inflight = activity.NewInflightExec()
	s.Instance.IdleReapEnabled = true
	s.Instance.IdleThresholdSeconds = 120
	var paused atomic.Bool
	pauses, creates := &atomic.Int32{}, &atomic.Int32{}
	var newMu sync.Mutex
	var created cube.Sandbox
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/sandboxes/vm-idle":
			state := "running"
			if paused.Load() {
				state = "paused"
			}
			json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: "vm-idle", TemplateID: "tpl-safe", State: state, CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_id": "idle-owned", "sandboxd_app_id": appID}})
		case r.Method == "POST" && r.URL.Path == "/sandboxes/vm-idle/pause":
			pauses.Add(1)
			if mode == "ambiguous" {
				w.WriteHeader(503)
				return
			}
			paused.Store(true)
			w.WriteHeader(204)
		case r.Method == "POST" && r.URL.Path == "/sandboxes":
			creates.Add(1)
			var in cube.CreateRequest
			json.NewDecoder(r.Body).Decode(&in)
			if in.Lifecycle == nil || in.Lifecycle.AutoResume {
				t.Error("creation lost no-auto-resume")
			}
			newMu.Lock()
			created = cube.Sandbox{SandboxID: "vm-new", TemplateID: in.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: in.Metadata}
			out := created
			newMu.Unlock()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(out)
		case r.Method == "GET" && r.URL.Path == "/sandboxes/vm-new":
			newMu.Lock()
			out := created
			newMu.Unlock()
			json.NewEncoder(w).Encode(out)
		default:
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(provider.Close)
	var e error
	s.Cube, e = cube.New(cube.Config{APIURL: provider.URL, APIKey: "synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = s.Cube.ConfigureAdmission(ctx, s.Store, cube.AdmissionConfig{MaxActive: 1, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{"tpl-safe": {CPUCount: 2, MemoryMB: 2048}}}); e != nil {
		t.Fatal(e)
	}
	a, e := s.Store.AdmissionBegin(ctx, "app:"+appID, "", "tpl-safe", "create", "owned-create")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Store.AdmissionFinish(ctx, a, "vm-idle", "active"); e != nil {
		t.Fatal(e)
	}
	policy := "sleep"
	if mode == "always-on" {
		policy = "always_on"
	}
	sb := &store.Sandbox{ID: "idle-owned", Status: "running", RuntimeProvider: "cube", IdlePolicy: policy, AppID: sql.NullString{String: appID, Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-idle", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: []byte("fixture"), TokenNonce: []byte("fixture")}}
	if e = s.Store.Create(ctx, sb); e != nil {
		t.Fatal(e)
	}
	last := time.Now().Add(-time.Hour)
	if mode == "recent" {
		last = time.Now()
	}
	if e = s.Store.BumpLastActive(ctx, sb.ID, last); e != nil {
		t.Fatal(e)
	}
	switch mode {
	case "keepalive":
		if e = s.Store.SetKeepaliveUntil(ctx, sb.ID, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
	case "task":
		if e = s.Store.CreateTask(ctx, &store.Task{TaskID: "owned-task", SandboxID: sb.ID, Agent: "claude-code", Prompt: "fixture"}); e != nil {
			t.Fatal(e)
		}
	case "stream":
		s.Inflight.Enter(sb.ID)
		t.Cleanup(func() { s.Inflight.Exit(sb.ID) })
	case "disabled":
		s.Instance.IdleReapEnabled = false
	case "pending":
		if _, e = s.Store.AdmissionBegin(ctx, "app:"+appID, "vm-idle", "tpl-safe", "connect", "pending-operation"); e != nil {
			t.Fatal(e)
		}
	}
	return s, pauses, creates
}

func TestCubeCapacityReclaimsOneConfirmedIdleGuestThenCreates(t *testing.T) {
	s, pauses, creates := capacityFixture(t, "")
	ctx := context.Background()
	attempts := 0
	err := s.withCubeCapacityRetry(ctx, "new-owned", func() error {
		attempts++
		_, e := s.Cube.Create(ctx, cube.CreateRequest{TemplateID: "tpl-safe", Metadata: map[string]string{"sandboxd_id": "new-owned", "sandboxd_app_id": "new-app"}, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}})
		return e
	})
	if err != nil || attempts != 2 || pauses.Load() != 1 || creates.Load() != 1 {
		t.Fatalf("attempts=%d pauses=%d creates=%d error=%v", attempts, pauses.Load(), creates.Load(), err)
	}
	row, _ := s.Store.Get(ctx, "idle-owned")
	a, _ := s.Store.AdmissionLookup(ctx, "vm-idle")
	if row.Status != "stopped" || a.Charged != 0 || a.State != "released" {
		t.Fatal("idle guest was not durably released")
	}
}
func TestCubeCapacityProtectsWorkAndAmbiguousPause(t *testing.T) {
	for _, mode := range []string{"always-on", "keepalive", "task", "stream", "recent", "disabled", "pending", "requested", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			s, pauses, _ := capacityFixture(t, mode)
			id := "new-owned"
			if mode == "requested" {
				id = "idle-owned"
			}
			attempts := 0
			e := s.withCubeCapacityRetry(context.Background(), id, func() error { attempts++; return cube.ErrCapacityUnavailable })
			if !errors.Is(e, cube.ErrCapacityUnavailable) || attempts != 1 {
				t.Fatal("protected/uncertain operation retried")
			}
			want := int32(0)
			if mode == "ambiguous" {
				want = 1
			}
			if pauses.Load() != want {
				t.Fatal("protected workload was paused")
			}
			a, _ := s.Store.AdmissionLookup(context.Background(), "vm-idle")
			if a.Charged != 1 {
				t.Fatal("unconfirmed pause freed a slot")
			}
		})
	}
}
func TestCubeCapacityDoesNotEvictForUnknownCreateOrPipelineBusy(t *testing.T) {
	for _, original := range []error{cube.ErrAdmissionPending, cube.ErrCreationBusy, errors.New("provider disconnected")} {
		s, pauses, _ := capacityFixture(t, "")
		attempts := 0
		e := s.withCubeCapacityRetry(context.Background(), "new-owned", func() error { attempts++; return original })
		if e != original || attempts != 1 || pauses.Load() != 0 {
			t.Fatal("uncertain/create-pipeline error caused retry or eviction")
		}
	}
}
func TestCubeCapacityIncomingActivityWinsLifecycleLock(t *testing.T) {
	s, pauses, _ := capacityFixture(t, "")
	ctx := context.Background()
	// An incoming ordinary preview registers under this exact lock. Reclamation
	// must neither wait behind it nor pause after registration and unlock.
	s.Locks.Lock("idle-owned")
	done := make(chan error, 1)
	go func() {
		done <- s.withCubeCapacityRetry(ctx, "new-owned", func() error { return cube.ErrCapacityUnavailable })
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		s.Locks.Unlock("idle-owned")
		t.Fatal("reclaimer waited for incoming preview")
	}
	s.Inflight.Enter("idle-owned")
	s.Locks.Unlock("idle-owned")
	defer s.Inflight.Exit("idle-owned")
	s.withCubeCapacityRetry(ctx, "new-owned", func() error { return cube.ErrCapacityUnavailable })
	if pauses.Load() != 0 {
		t.Fatal("incoming activity lost eviction race")
	}
}

func TestCubeCapacityUnknownIdlePolicyIsNotEligible(t *testing.T) {
	now := time.Now()
	sb := &store.Sandbox{ID: "idle", RuntimeProvider: "cube", Status: "running", IdlePolicy: "future-policy", LastActiveAt: now.Add(-time.Hour)}
	if idleCubeCandidate(sb, "other", now.Add(-2*time.Minute), now) {
		t.Fatal("unknown idle policy must not permit eviction")
	}
	sb.IdlePolicy = "sleep"
	if !idleCubeCandidate(sb, "other", now.Add(-2*time.Minute), now) {
		t.Fatal("explicit sleep policy should permit idle candidate selection")
	}
}

func TestCubeStorageRefusalDoesNotEvictOrRetry(t *testing.T) {
	s, pauses, creates := capacityFixture(t, "")
	attempts := 0
	e := s.withCubeCapacityRetry(context.Background(), "new-owned", func() error { attempts++; return cube.ErrStorageUnavailable })
	if !errors.Is(e, cube.ErrStorageUnavailable) || attempts != 1 || pauses.Load() != 0 || creates.Load() != 0 {
		t.Fatal("storage refusal triggered capacity eviction/retry", e)
	}
}
