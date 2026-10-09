package cube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func observationFixture() map[string]any {
	return map[string]any{"ret": map[string]any{"ret_code": 200}, "data": []any{map[string]any{"sandbox_id": "vm-one", "host_id": "node-one", "template_id": "tpl-one", "status": 5, "end_at": int64(1800000000000), "labels": map[string]string{"sandboxd_id": "owned"}, "containers": []any{map[string]any{"container_id": "vm-one", "cpu_milli": 2000, "memory_mib": 2048}}}}}
}
func TestMasterObservationChecksIdentityResourcesAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		change      func(map[string]any)
		want        bool
		unavailable bool
	}{
		{"valid", func(map[string]any) {}, true, false},
		{"wrong identity", func(r map[string]any) { r["sandbox_id"] = "other" }, false, false},
		{"missing status", func(r map[string]any) { delete(r, "status") }, false, false},
		{"crashed", func(r map[string]any) { r["status"] = 2 }, false, true},
		{"unknown", func(r map[string]any) { r["status"] = 0 }, false, true},
		{"missing resources", func(r map[string]any) { r["containers"] = []any{} }, false, false},
		{"fractional resources", func(r map[string]any) { r["containers"].([]any)[0].(map[string]any)["cpu_milli"] = 2500 }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := observationFixture()
			tc.change(v["data"].([]any)[0].(map[string]any))
			raw, _ := json.Marshal(v)
			out, e := decodeMasterObservation(raw, "vm-one")
			if (e == nil) != tc.want {
				t.Fatalf("unexpected result %v %v", out, e)
			}
			if tc.unavailable && !errors.Is(e, ErrRuntimeUnavailable) {
				t.Fatal(e)
			}
			if tc.want && (out.CPUCount != 2 || out.MemoryMB != 2048 || out.EndAt.UnixMilli() != 1800000000000 || out.Metadata["sandboxd_id"] != "owned") {
				t.Fatal("observation lost required fields")
			}
		})
	}
}
func TestPlacedObservationUsesOneRequestAndRejectsDifferentWorker(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Caller") != "baarcha-controller" {
			t.Error("observation shares the bulk inventory worker connection")
		}
		if r.URL.Path != "/cube/sandbox/info" {
			t.Error("unexpected public summary lookup")
		}
		json.NewEncoder(w).Encode(observationFixture())
	}))
	defer server.Close()
	c, e := New(Config{APIURL: server.URL, APIKey: "synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ConfigurePlacement(server.URL, "cubebox"); e != nil {
		t.Fatal(e)
	}
	c.admission = &admissionGuard{config: AdmissionConfig{NodeID: "node-one"}}
	out, e := c.getPlaced(context.Background(), "vm-one")
	if e != nil || out.State != "paused" || requests.Load() != 1 {
		t.Fatalf("observation duplicated or failed: %v %d", e, requests.Load())
	}
	c.admission.config.NodeID = "node-two"
	if _, e = c.getPlaced(context.Background(), "vm-one"); !errors.Is(e, ErrAdmissionPending) {
		t.Fatal("wrong worker accepted")
	}
}

// Keep the readiness hook bounded by its caller's cancellation on the legacy path.
func TestConnectCheckRejectsReadinessFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Sandbox{SandboxID: "vm-one", TemplateID: "tpl-one"})
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	expected := errors.New("supervisor not ready")
	_, err := c.ConnectAndCheck(ctx, "vm-one", ConnectRequest{}, func(context.Context) error { return expected })
	if !errors.Is(err, expected) {
		t.Fatal("readiness failure lost")
	}
}

type startupLedger struct {
	AdmissionStore
	finished atomic.Bool
}

func (l *startupLedger) AdmissionLookup(context.Context, string) (AdmissionRecord, error) {
	return AdmissionRecord{Key: "owned", RuntimeID: "vm-one", State: "released"}, nil
}
func (l *startupLedger) AdmissionBegin(context.Context, string, string, string, string, string) (AdmissionRecord, error) {
	return AdmissionRecord{Key: "owned", RuntimeID: "vm-one", State: "pending"}, nil
}
func (l *startupLedger) AdmissionFinish(context.Context, AdmissionRecord, string, string) error {
	l.finished.Store(true)
	return nil
}
func TestConnectReadinessOverlapsVerificationWithoutBypassingAdmission(t *testing.T) {
	for _, mode := range []string{"success", "wrong-worker", "not-ready"} {
		t.Run(mode, func(t *testing.T) {
			var connected atomic.Bool
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				connected.Store(true)
				json.NewEncoder(w).Encode(Sandbox{SandboxID: "vm-one", TemplateID: "tpl-one"})
			})
			ledger := &startupLedger{}
			c.admission = &admissionGuard{store: ledger, config: AdmissionConfig{NodeID: "node-one", Templates: map[string]AdmissionResources{"tpl-one": {CPUCount: 2, MemoryMB: 2048}}}}
			verified, checking := make(chan struct{}), make(chan struct{})
			var reads atomic.Int32
			c.observation = func(ctx context.Context, id string) (*Sandbox, error) {
				value := &Sandbox{SandboxID: id, TemplateID: "tpl-one", ClientID: "node-one", CPUCount: 2, MemoryMB: 2048, State: "paused"}
				if reads.Add(1) > 1 {
					close(verified)
					select {
					case <-checking:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					value.State = "running"
					if mode == "wrong-worker" {
						value.ClientID = "other"
					}
				}
				return value, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			expected := errors.New("not ready")
			_, err := c.ConnectAndCheck(ctx, "vm-one", ConnectRequest{}, func(ctx context.Context) error {
				if !connected.Load() {
					t.Error("readiness before authorized provider mutation")
				}
				close(checking)
				select {
				case <-verified:
				case <-ctx.Done():
					return ctx.Err()
				}
				if mode == "wrong-worker" {
					<-ctx.Done()
					return ctx.Err()
				}
				if mode == "not-ready" {
					return expected
				}
				return nil
			})
			switch mode {
			case "success":
				if err != nil || !ledger.finished.Load() {
					t.Fatal(err)
				}
			case "wrong-worker":
				if !errors.Is(err, ErrAdmissionPending) || ledger.finished.Load() {
					t.Fatal("placement bypassed", err)
				}
			case "not-ready":
				if !errors.Is(err, expected) || !ledger.finished.Load() {
					t.Fatal("readiness failure lost reservation", err)
				}
			}
			if reads.Load() != 2 {
				t.Fatal("duplicate authoritative reads", reads.Load())
			}
		})
	}
}

func TestObservedPausedRuntimeUsesSingleNativeResumeWithBothChecks(t *testing.T) {
	var gets, updates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Caller") != "baarcha-controller" {
			t.Error("startup shares the bulk inventory worker connection")
		}
		switch r.URL.Path {
		case "/cube/sandbox/info":
			gets.Add(1)
			v := observationFixture()
			if updates.Load() > 0 {
				v["data"].([]any)[0].(map[string]any)["status"] = 1
			}
			json.NewEncoder(w).Encode(v)
		case "/cube/sandbox/update":
			updates.Add(1)
			var body struct {
				ID      string `json:"sandbox_id"`
				Action  string `json:"action"`
				Timeout int    `json:"timeout"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.ID != "vm-one" || body.Action != "resume" || body.Timeout != 3600 {
				t.Error("wrong native mutation")
			}
			json.NewEncoder(w).Encode(map[string]any{"ret": map[string]int{"ret_code": 200}})
		default:
			t.Error("redundant public API call", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	c, e := New(Config{APIURL: server.URL, APIKey: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ConfigurePlacement(server.URL, "cubebox"); e != nil {
		t.Fatal(e)
	}
	ledger := &startupLedger{}
	c.admission = &admissionGuard{store: ledger, config: AdmissionConfig{NodeID: "node-one", Templates: map[string]AdmissionResources{"tpl-one": {CPUCount: 2, MemoryMB: 2048}}}}
	value, e := c.Connect(context.Background(), "vm-one", ConnectRequest{TimeoutSeconds: 3600})
	if e != nil || value.State != "running" || gets.Load() != 2 || updates.Load() != 1 || !ledger.finished.Load() {
		t.Fatalf("resume failed/repeated: %v reads=%d writes=%d", e, gets.Load(), updates.Load())
	}
}

func TestNativeResumeRetriesOnlyExplicitPreMutationConcurrencyRejections(t *testing.T) {
	for _, kind := range []string{"busy-then-success", "unknown-code", "wrong-flow", "bad-json", "http-error", "dropped-response", "cancelled-busy"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				if kind == "dropped-response" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				if kind == "http-error" {
					w.WriteHeader(503)
					return
				}
				if kind == "bad-json" {
					w.Write([]byte("broken"))
					return
				}
				code := 130513
				msg := "flow [create] exceed limited"
				if kind == "busy-then-success" && n == 2 {
					code = 200
				}
				if kind == "unknown-code" {
					code = 130597
				}
				if kind == "wrong-flow" {
					msg = "different operation"
				}
				json.NewEncoder(w).Encode(map[string]any{"ret": map[string]any{"ret_code": code, "ret_msg": msg}})
			}))
			defer server.Close()
			client, err := New(Config{APIURL: server.URL, APIKey: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			if err = client.ConfigurePlacement(server.URL, "cubebox"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if kind == "cancelled-busy" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
			}
			defer cancel()
			err = client.resumeObserved(ctx, "vm-one", ConnectRequest{TimeoutSeconds: 3600})
			if kind == "busy-then-success" {
				if err != nil || requests.Load() != 2 {
					t.Fatalf("resume: %v requests=%d", err, requests.Load())
				}
				return
			}
			if err == nil || requests.Load() != 1 {
				t.Fatalf("unsafe retry: %v requests=%d", err, requests.Load())
			}
			if (kind == "cancelled-busy") != errors.Is(err, errNativeResumeBusy) {
				t.Fatalf("ambiguous result labeled safe to release: %v", err)
			}
		})
	}
}

type rejectedResumeLedger struct {
	AdmissionStore
	begins   int
	finishes []string
}

func (l *rejectedResumeLedger) AdmissionLookup(context.Context, string) (AdmissionRecord, error) {
	return AdmissionRecord{Key: "app:one", RuntimeID: "vm-one", TemplateID: "tpl-one", State: "released"}, nil
}
func (l *rejectedResumeLedger) AdmissionBegin(context.Context, string, string, string, string, string) (AdmissionRecord, error) {
	l.begins++
	return AdmissionRecord{Key: "app:one", RuntimeID: "vm-one", TemplateID: "tpl-one", State: "pending", Token: "single-lease", Charged: 1}, nil
}
func (l *rejectedResumeLedger) AdmissionFinish(_ context.Context, a AdmissionRecord, _ string, state string) error {
	if a.Token != "single-lease" {
		return errors.New("wrong lease")
	}
	l.finishes = append(l.finishes, state)
	return nil
}
func TestExhaustedRejectedResumeReleasesOnlyAuthoritativelyPausedLease(t *testing.T) {
	for _, mode := range []string{"paused", "running", "unknown", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			client, err := New(Config{APIURL: "http://127.0.0.1:1", APIKey: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			ledger := &rejectedResumeLedger{}
			client.admission = &admissionGuard{store: ledger, config: AdmissionConfig{NodeID: "node-one", Templates: map[string]AdmissionResources{"tpl-one": {CPUCount: 1, MemoryMB: 768}}}}
			reads := 0
			client.observation = func(context.Context, string) (*Sandbox, error) {
				reads++
				if reads > 1 && mode == "unknown" {
					return nil, errors.New("offline")
				}
				state := "paused"
				if reads > 1 && mode == "running" {
					state = "running"
				}
				return &Sandbox{SandboxID: "vm-one", TemplateID: "tpl-one", ClientID: "node-one", CPUCount: 1, MemoryMB: 768, State: state}, nil
			}
			client.resumeObserved = func(context.Context, string, ConnectRequest) error {
				if mode == "ambiguous" {
					return errors.New("lost response")
				}
				return errNativeResumeBusy
			}
			_, err = client.admittedConnect(context.Background(), "vm-one", ConnectRequest{}, nil)
			if ledger.begins != 1 {
				t.Fatal("more than one reservation")
			}
			if mode == "paused" {
				if !errors.Is(err, ErrCreationBusy) || len(ledger.finishes) != 1 || ledger.finishes[0] != "released" {
					t.Fatalf("safe release: %v %+v", err, ledger)
				}
			} else if !errors.Is(err, ErrAdmissionPending) || len(ledger.finishes) != 0 {
				t.Fatalf("uncertain outcome released: %v %+v", err, ledger)
			}
		})
	}
}
