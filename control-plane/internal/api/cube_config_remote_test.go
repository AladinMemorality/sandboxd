package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeConfigEncryptedPendingApplyReplaceAndOwnerBoundary(t *testing.T) {
	var mu sync.Mutex
	revision := ""
	var applied []runtime.AppConfigRequest
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/config" {
			var req runtime.AppConfigRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			applied = append(applied, req)
			revision = req.Revision
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(runtime.Status{AppConfigRevision: revision})
	})
	sb, err := s.Store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + sb.AppID.String + "/config"
	for _, body := range []string{
		`{"key":"DATA_KEY","value":"initial-secret","sensitive":true,"access_policy":"runtime_access"}`,
		`{"key":"CONTROL_KEY","value":"control-secret","sensitive":true,"access_policy":"control_plane_only"}`,
		`{"key":"AGENT_KEY","value":"agent-secret","sensitive":true,"access_policy":"agent_access"}`,
	} {
		w := cubeRequest(s, "POST", base, body, cfgTenant)
		if w.Code != 201 || !strings.Contains(w.Body.String(), `"runtime_apply_status":"pending"`) {
			t.Fatalf("pending save: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("secret leaked in saved config response")
		}
	}
	saved, err := s.Store.GetAppConfig(context.Background(), sb.AppID.String, "DATA_KEY")
	if err != nil || saved.ValuePlaintext.Valid || len(saved.ValueCiphertext) == 0 {
		t.Fatalf("plaintext stored: %+v %v", saved, err)
	}
	mu.Lock()
	count := len(applied)
	mu.Unlock()
	if count != 0 {
		t.Fatal("config write unexpectedly woke stopped VM")
	}
	w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/start", "", cfgTenant)
	if w.Code != 200 {
		t.Fatalf("start apply: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	first := applied[0]
	mu.Unlock()
	if len(first.Env) != 1 || first.Env["DATA_KEY"] != "initial-secret" {
		t.Fatalf("wrong config policy delivery: keys %v", first.Env)
	}
	b, err := s.Store.GetRuntimeBinding(context.Background(), id)
	if err != nil || b.ConfigRevision != b.ConfigAppliedRevision {
		t.Fatal("applied revision not acknowledged")
	}
	w = cubeRequest(s, "PATCH", base+"/DATA_KEY", `{"value":"rotated-secret"}`, cfgTenant)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"runtime_apply_status":"applied"`) {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	last := applied[len(applied)-1]
	mu.Unlock()
	if last.Env["DATA_KEY"] != "rotated-secret" || last.Revision == first.Revision {
		t.Fatal("rotation not applied")
	}
	w = cubeRequest(s, "DELETE", base+"/DATA_KEY", "", cfgTenant)
	if w.Code != 204 || w.Header().Get("X-Sandboxd-Runtime-Config") != "applied" {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	last = applied[len(applied)-1]
	mu.Unlock()
	if len(last.Env) != 0 {
		t.Fatal("deleted runtime key retained in guest replacement")
	}
	for _, method := range []string{"GET", "POST"} {
		w = cubeRequest(s, method, base, `{"key":"X","value":"x"}`, "other-owner")
		if w.Code != 404 {
			t.Fatalf("cross-owner config %s: %d", method, w.Code)
		}
	}
	w = cubeRequest(s, "POST", base, `{"key":"RUNTIMED_HTTP_TOKEN","value":"replace-control-token","access_policy":"runtime_access"}`, cfgTenant)
	if w.Code != 400 {
		t.Fatalf("reserved supervisor environment accepted: %d", w.Code)
	}
	if _, err := s.Store.GetAppConfig(context.Background(), sb.AppID.String, "RUNTIMED_HTTP_TOKEN"); err != store.ErrNotFound {
		t.Fatal("reserved value persisted")
	}
}

func TestCubeConfigWaitsForTaskAndReconcilesAfterCompletion(t *testing.T) {
	var mu sync.Mutex
	revision := ""
	applies := 0
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/config" {
			var req runtime.AppConfigRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			revision = req.Revision
			applies++
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(runtime.Status{AppConfigRevision: revision})
	})
	if err := s.Store.MarkRunning(context.Background(), id, "", ""); err != nil {
		t.Fatal(err)
	}
	taskID := "01M2QHT40D9S9W5MFNN32F1DXW"
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: taskID, SandboxID: id, Agent: "opencode", Prompt: "working"}); err != nil {
		t.Fatal(err)
	}
	sb, _ := s.Store.Get(context.Background(), id)
	w := cubeRequest(s, "POST", "/v1/apps/"+sb.AppID.String+"/config", `{"key":"COLOR","value":"blue","access_policy":"runtime_access"}`, cfgTenant)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"runtime_apply_status":"pending"`) {
		t.Fatalf("active save: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	count := applies
	mu.Unlock()
	if count != 0 {
		t.Fatal("restarted during active task")
	}
	if err := s.Store.FinishTask(context.Background(), taskID, "succeeded", `{"status":"succeeded"}`); err != nil {
		t.Fatal(err)
	}
	s.ReconcileCube(context.Background())
	b, err := s.Store.GetRuntimeBinding(context.Background(), id)
	if err != nil || b.ConfigRevision != b.ConfigAppliedRevision {
		t.Fatalf("pending config not recovered: %+v %v", b, err)
	}
	mu.Lock()
	count = applies
	mu.Unlock()
	if count != 1 {
		t.Fatalf("config applied %d times", count)
	}
}
