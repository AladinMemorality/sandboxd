package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/activity"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeReconcilePreservesRunningPoliciesAndManualStop(t *testing.T) {
	for _, tc := range []struct {
		name, local, remote, policy string
		keepalive, stream           bool
		want                        int32
	}{
		{"sleep-idle", "running", "running", "sleep", false, false, 0},
		{"sleep-reaping-disabled", "running", "running", "sleep", false, false, 0},
		{"sleep-pause-failed", "running", "running", "sleep", false, false, 0},
		{"active-task", "running", "running", "sleep", false, false, 1},
		{"active-task-paused", "stopped", "paused", "sleep", false, false, 1},
		{"always-on", "running", "running", "always_on", false, false, 1},
		{"always-on-auto-paused", "running", "paused", "always_on", false, false, 1},
		{"always-on-manually-stopped", "stopped", "paused", "always_on", false, false, 0},
		{"keepalive", "running", "running", "sleep", true, false, 1},
		{"keepalive-auto-paused", "running", "paused", "sleep", true, false, 1},
		{"keepalive-manually-stopped", "stopped", "paused", "sleep", true, false, 0},
		{"preview-stream", "running", "running", "sleep", false, true, 1},
		{"preview-stream-auto-paused", "running", "paused", "sleep", false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, appID := newConfigTestServer(t)
			var connects, pauses atomic.Int32
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && r.URL.Path == "/sandboxes/vm-retained/pause" {
					pauses.Add(1)
					if tc.name == "sleep-pause-failed" {
						w.WriteHeader(503)
					} else {
						w.WriteHeader(204)
					}
					return
				}
				if r.Method == "POST" && r.URL.Path == "/sandboxes/vm-retained/connect" {
					var req cube.ConnectRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					wantLease := 3600
					if strings.HasPrefix(tc.name, "active-task") {
						wantLease = 86400 + 600
					}
					if req.TimeoutSeconds != wantLease {
						t.Errorf("lease %d, want %d", req.TimeoutSeconds, wantLease)
					}
					connects.Add(1)
				} else if r.Method != "GET" || r.URL.Path != "/sandboxes/vm-retained" {
					t.Errorf("unexpected provider mutation %s %s", r.Method, r.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]string{"sandboxID": "vm-retained", "templateID": "tpl-safe", "state": tc.remote})
			}))
			defer management.Close()
			var err error
			s.Cube, err = cube.New(cube.Config{APIURL: management.URL, APIKey: "fixture-management"})
			if err != nil {
				t.Fatal(err)
			}
			guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
			defer guest.Close()
			s.CubeProxyURL = guest.URL
			s.Inflight = activity.NewInflightExec()
			plain, _ := json.Marshal(cubeCredentials{SupervisorToken: strings.Repeat("a", 64), TrafficAccessToken: "fixture-ingress"})
			sealed, nonce, err := s.Secrets.Seal(plain)
			if err != nil {
				t.Fatal(err)
			}
			sb := &store.Sandbox{ID: "cube-retained", Status: tc.local, RuntimeProvider: "cube", IdlePolicy: tc.policy,
				AppID:          sql.NullString{String: appID, Valid: true},
				RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-retained", TemplateID: "tpl-safe", Domain: "cube.test", TokenCiphertext: sealed, TokenNonce: nonce}}
			ctx := context.Background()
			if err = s.Store.Create(ctx, sb); err != nil {
				t.Fatal(err)
			}
			s.Instance.IdleReapEnabled = tc.name != "sleep-reaping-disabled"
			s.Instance.IdleThresholdSeconds = 60
			if err = s.Store.BumpLastActive(ctx, sb.ID, time.Now().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			if tc.keepalive {
				if err = s.Store.SetKeepaliveUntil(ctx, sb.ID, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.stream {
				s.Inflight.Enter(sb.ID)
				defer s.Inflight.Exit(sb.ID)
			}
			if strings.HasPrefix(tc.name, "active-task") {
				if err = s.Store.CreateTask(ctx, &store.Task{TaskID: "cube-retained-task", SandboxID: sb.ID, Agent: "claude-code", Prompt: "fixture"}); err != nil {
					t.Fatal(err)
				}
			}
			s.ReconcileCube(ctx)
			if got := connects.Load(); got != tc.want {
				t.Fatalf("connects %d, want %d", got, tc.want)
			}
			row, err := s.Store.Get(ctx, sb.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.local == "stopped" && !strings.HasPrefix(tc.name, "active-task") && row.Status != "stopped" {
				t.Fatal("manual stop was undone")
			}
			wantPauses := int32(0)
			if tc.name == "sleep-idle" || tc.name == "sleep-pause-failed" {
				wantPauses = 1
			}
			if pauses.Load() != wantPauses {
				t.Fatalf("pause calls %d, want %d", pauses.Load(), wantPauses)
			}
			if tc.name == "sleep-idle" && row.Status != "stopped" {
				t.Fatal("idle Cube guest was not paused")
			}
			if tc.name == "sleep-pause-failed" && row.Status != "running" {
				t.Fatal("failed provider pause fabricated a stopped state")
			}
		})
	}
}

func TestCubePreviewTracksOpenStreams(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s, _, _ := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "stream-start")
		w.(http.Flusher).Flush()
		close(entered)
		<-release
	})
	s.Inflight = activity.NewInflightExec()
	go func() {
		defer close(done)
		s.TryServeCubePreview(httptest.NewRecorder(), cubePreviewRequest(t, "GET", "/stream", ""))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("stream did not open")
	}
	active := s.Inflight.Active(cubePreviewTestID)
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close")
	}
	if !active {
		t.Fatal("open stream not retained by maintenance")
	}
	if s.Inflight.Active(cubePreviewTestID) {
		t.Fatal("closed stream leaked an active lease")
	}
}

func TestCubeReconcileRecoveryPreservesDataAndChargedAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, state                                 string
		foreign, unavailable, missing, wantRecovery bool
	}{
		{name: "unknown", state: "unknown", wantRecovery: true},
		{name: "stopped", state: "stopped", wantRecovery: true},
		{name: "provider-outage", unavailable: true},
		{name: "known-runtime-missing", missing: true, wantRecovery: true},
		{name: "foreign-identity", state: "unknown", foreign: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, appID := newConfigTestServer(t)
			ctx := context.Background()
			var mutations, guestCalls atomic.Int32
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/sandboxes/vm-recovery" {
					mutations.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if tc.missing {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if tc.unavailable {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				id := "vm-recovery"
				if tc.foreign {
					id = "vm-other-owner"
				}
				_ = json.NewEncoder(w).Encode(cube.Sandbox{SandboxID: id, TemplateID: "tpl-reviewed", State: tc.state, CPUCount: 2, MemoryMB: 2048})
			}))
			defer management.Close()
			var err error
			s.Cube, err = cube.New(cube.Config{APIURL: management.URL, APIKey: "fixture-management"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Cube.ConfigureAdmission(ctx, s.Store, cube.AdmissionConfig{
				MaxActive: 1, CPUCount: 2, MemoryMB: 2048,
				Templates: map[string]cube.AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}},
			}); err != nil {
				t.Fatal(err)
			}
			reservation, err := s.Store.AdmissionBegin(ctx, "app:"+appID, "", "tpl-reviewed", "create", "retained-operation")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Store.AdmissionFinish(ctx, reservation, "vm-recovery", "active"); err != nil {
				t.Fatal(err)
			}
			admissionBefore, err := s.Store.AdmissionLookup(ctx, "vm-recovery")
			if err != nil {
				t.Fatal(err)
			}
			guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { guestCalls.Add(1); w.WriteHeader(500) }))
			defer guest.Close()
			s.CubeProxyURL = guest.URL
			plain, err := json.Marshal(cubeCredentials{SupervisorToken: strings.Repeat("a", 64), TrafficAccessToken: "retained-private-ingress"})
			if err != nil {
				t.Fatal(err)
			}
			sealed, nonce, err := s.Secrets.Seal(plain)
			if err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			marker := filepath.Join(workspace, "acknowledged-write")
			if err = os.WriteFile(marker, []byte("keep latest durable data"), 0600); err != nil {
				t.Fatal(err)
			}
			sb := &store.Sandbox{ID: "cube-recovery", Status: "running", RuntimeProvider: "cube", IdlePolicy: "always_on", WorkspaceMnt: workspace,
				AppID: sql.NullString{String: appID, Valid: true}, ExternalUserID: sql.NullString{String: "owner-recovery", Valid: true},
				RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-recovery", TemplateID: "tpl-reviewed", Domain: "cube.test", TokenCiphertext: sealed, TokenNonce: nonce}}
			if err = s.Store.Create(ctx, sb); err != nil {
				t.Fatal(err)
			}
			if err = s.Store.CreateTask(ctx, &store.Task{TaskID: "recovery-retained-task", SandboxID: sb.ID, Agent: "claude-code", Prompt: "retained task history"}); err != nil {
				t.Fatal(err)
			}
			bindingBefore, err := s.Store.GetRuntimeBinding(ctx, sb.ID)
			if err != nil {
				t.Fatal(err)
			}
			s.cubePreviewLeases.Store(sb.ID, time.Now().Add(time.Minute))
			// Exercise the real HTTP decoder and typed provider error, rather
			// than a mock that bypasses upstream identity/state validation.
			if _, err = s.Cube.Get(ctx, "vm-recovery"); errors.Is(err, cube.ErrRuntimeUnavailable) != tc.wantRecovery {
				t.Fatalf("typed recovery error=%v, want %v", err, tc.wantRecovery)
			}
			s.ReconcileCube(ctx)
			row, err := s.Store.Get(ctx, sb.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := "running"
			if tc.wantRecovery {
				wantStatus = "error"
			}
			if row.Status != wantStatus {
				t.Fatalf("status=%q want %q", row.Status, wantStatus)
			}
			if tc.wantRecovery && row.ErrorMessage.String != "Cube runtime requires operator recovery; binding retained" {
				t.Fatalf("unexpected public recovery message %q", row.ErrorMessage.String)
			}
			if _, present := s.cubePreviewLeases.Load(sb.ID); present == tc.wantRecovery {
				t.Fatalf("preview lease present=%v", present)
			}
			if tc.wantRecovery {
				request := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/"+sb.ID, nil)
				request.SetPathValue("id", sb.ID)
				response := httptest.NewRecorder()
				s.v1GetSandbox(response, request)
				var public v1Sandbox
				if err := json.Unmarshal(response.Body.Bytes(), &public); err != nil {
					t.Fatal(err)
				}
				if response.Code != 200 || public.Status != "error" || public.ErrorCode != "runtime_recovery_required" || public.Preview.Status != "down" {
					t.Fatalf("recovery status not exposed truthfully: %s", response.Body.String())
				}
				if strings.Contains(response.Body.String(), "retained-private-ingress") || strings.Contains(response.Body.String(), workspace) {
					t.Fatal("recovery response leaked private data")
				}
			}
			bindingAfter, err := s.Store.GetRuntimeBinding(ctx, sb.ID)
			if err != nil || !reflect.DeepEqual(bindingBefore, bindingAfter) {
				t.Fatal("runtime identity or encrypted credentials changed", err)
			}
			if row.AppID != sb.AppID || row.ExternalUserID != sb.ExternalUserID || row.WorkspaceMnt != workspace {
				t.Fatal("owner/app/workspace binding changed")
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "keep latest durable data" {
				t.Fatal("retained data changed", err)
			}
			if active, err := s.Store.SandboxHasRunningTask(ctx, sb.ID); err != nil || !active {
				t.Fatal("task history/state changed", err)
			}
			admissionAfter, err := s.Store.AdmissionLookup(ctx, "vm-recovery")
			if err != nil || admissionBefore != admissionAfter || admissionAfter.Charged != 1 {
				t.Fatal("recovery released or modified charged admission", err)
			}
			if _, err = s.Store.AdmissionBegin(ctx, "app:another", "", "tpl-reviewed", "create", "must-refuse"); !errors.Is(err, cube.ErrCapacityUnavailable) {
				t.Fatalf("retained slot did not refuse another create: %v", err)
			}
			if mutations.Load() != 0 || guestCalls.Load() != 0 {
				t.Fatalf("unexpected mutation/guest calls: %d/%d", mutations.Load(), guestCalls.Load())
			}
		})
	}
}
