package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func codingQueueServer(t *testing.T, guest http.HandlerFunc) (*Server, string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if guest != nil {
			guest(w, r)
		} else {
			t.Error("queued task contacted guest")
			http.Error(w, "unexpected", 500)
		}
	})
	cfg := cube.AdmissionConfig{NodeID: "10.0.2.15", MaxActive: 52, HostCPUMillis: 72000, HostMemoryMB: 45056, Templates: map[string]cube.AdmissionResources{"tpl-tasks": {CPUCount: 1, MemoryMB: 512}}, ResourceBudget: &cube.ResourceBudget{CPUMillis: 9000, MemoryMB: 40960, RuntimeSlots: 50, BuildSlots: 2, Profiles: map[string]cube.ResourceProfile{"tpl-tasks": {CPUMillis: 100, WritableDiskMB: 4096, Kind: "runtime"}}}}
	ctx := context.Background()
	if _, err := s.Store.DB().Exec(`INSERT INTO cube_admission(admission_key,runtime_id,template_id,operation,token,state,charged,worker_id) VALUES('fixture','vm-tasks','tpl-tasks','pause','fixture','released',0,'vps')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.AdmissionPolicy(ctx, 52, "resource-budget-v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ConfigureResourceBudget(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	s.CubeTaskConcurrency = 50
	return s, id, &calls
}
func queueRequest(t *testing.T, s *Server, id string) string {
	t.Helper()
	w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/tasks", `{"prompt":"test queue only","agent":"opencode","env":{"EXAMPLE_SECRET":"private-request-value"}}`, cfgTenant)
	if w.Code != 202 {
		t.Fatalf("submit %d: %s", w.Code, w.Body.String())
	}
	var v struct{ ID, Status string }
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Status != "queued" {
		t.Fatal(v.Status)
	}
	return v.ID
}
func TestCodingQueueCancelAndReplayNeverWakeGuest(t *testing.T) {
	s, id, calls := codingQueueServer(t, nil)
	task := queueRequest(t, s, id)
	base := "/v1/sandboxes/" + id + "/tasks/" + task
	var ciphertext, nonce []byte
	if err := s.Store.DB().QueryRow(`SELECT ciphertext,nonce FROM cube_task_queue WHERE task_id=?`, task).Scan(&ciphertext, &nonce); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "private-request-value") {
		t.Fatal("request stored unencrypted")
	}
	raw, err := s.Secrets.Open(ciphertext, nonce)
	if err != nil || !strings.Contains(string(raw), "private-request-value") {
		t.Fatal("encrypted request cannot be restored", err)
	}
	if w := cubeRequest(s, "GET", base, "", cfgTenant); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"queued"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := cubeRequest(s, "POST", base+"/messages", `{"message_id":"12345678-1234-4234-8234-123456789abc","prompt":"additional request"}`, cfgTenant); w.Code != 409 || !strings.Contains(w.Body.String(), "task_queued") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := cubeRequest(s, "POST", base+"/cancel", "", cfgTenant); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := cubeRequest(s, "GET", base+"/events", "", cfgTenant); w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") || !strings.Contains(w.Body.String(), `"status":"cancelled"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := cubeRequest(s, "GET", base+"/events?since=1", "", cfgTenant); w.Code != 200 || strings.Contains(w.Body.String(), "event: done") {
		t.Fatal("terminal event replayed past cursor", w.Code, w.Body.String())
	}
	if w := cubeRequest(s, "GET", base+"/events", "", "different-owner"); w.Code != 404 && w.Code != 403 {
		t.Fatal("cross-owner replay permitted", w.Code)
	}
	var n int
	s.Store.DB().QueryRow(`SELECT count(*) FROM cube_task_queue WHERE task_id=?`, task).Scan(&n)
	if n != 0 || calls.Load() != 0 {
		t.Fatal("payload retained or guest contacted", n, calls.Load())
	}
}
func TestCodingQueuePreparationFailureReplaysLocally(t *testing.T) {
	s, id, calls := codingQueueServer(t, nil)
	task := queueRequest(t, s, id)
	ctx := context.Background()
	claim, err := s.Store.ClaimCubeTask(ctx, "claim", 50)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(failedResult(task, "sandbox_unavailable", "preparation failed"))
	if err = s.Store.FinishUndispatchedCubeTask(ctx, *claim, string(raw)); err != nil {
		t.Fatal(err)
	}
	w := cubeRequest(s, "GET", "/v1/sandboxes/"+id+"/tasks/"+task+"/events", "", cfgTenant)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"failed"`) || calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String(), calls.Load())
	}
}
func TestCodingQueueWaitingStreamKeepsResumeCursor(t *testing.T) {
	var since atomic.Int32
	s, id, _ := codingQueueServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			t.Error("unexpected guest route", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		if r.URL.Query().Get("since") != "5" {
			t.Error("cursor lost", r.URL.RawQuery)
		} else {
			since.Store(5)
		}
		json.NewEncoder(w).Encode(runtime.Event{ID: 5, Type: runtime.EventMessage, Data: json.RawMessage(`{"text":"resumed"}`)})
	})
	task := queueRequest(t, s, id)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Handler().ServeHTTP(w, r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: cfgTenant, Kind: "service"})))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/v1/sandboxes/%s/tasks/%s/events", server.URL, id, task), nil)
	req.Header.Set("Last-Event-ID", "4")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	claim, err := s.Store.ClaimCubeTask(ctx, "claim", 50)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	if err = s.Store.BeginCubeTaskDispatch(ctx, *claim); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || since.Load() != 5 || !strings.Contains(string(body), "id: 5") {
		t.Fatal(string(body), err, since.Load())
	}
}
