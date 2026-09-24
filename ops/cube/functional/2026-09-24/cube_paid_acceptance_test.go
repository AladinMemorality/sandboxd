package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Explicitly invoked disposable functional harness, not production startup or
// deployment isolation acceptance. No guest NIC or public egress is enabled.
func TestOperatorCubeClaudeFunctional(t *testing.T) {
	if os.Getenv("CUBE_OPERATOR_FUNCTIONAL") != "1" {
		t.Skip("explicit operator fixture only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var secret struct {
		CubeKey string `json:"cube_key"`
	}
	data, err := os.ReadFile("/root/cube-pilot/test-secrets.json")
	if err != nil {
		t.Fatal("Cube fixture credentials unavailable")
	}
	if json.Unmarshal(data, &secret) != nil || secret.CubeKey == "" {
		t.Fatal("Cube fixture credentials invalid")
	}
	var fixture struct {
		Project     string `json:"project"`
		Owner       int    `json:"owner"`
		BridgeToken string `json:"bridgeToken"`
		FileID      string `json:"fileId"`
	}
	data, err = os.ReadFile("/root/cube-claude-acceptance/fixture.json")
	if err != nil {
		t.Fatal("functional fixture unavailable")
	}
	if json.Unmarshal(data, &fixture) != nil {
		t.Fatal("functional fixture invalid")
	}
	s, _ := newConfigTestServer(t)
	s.Locks = idlock.New()
	s.Cube, err = cube.New(cube.Config{APIURL: "http://127.0.0.1:3000", APIKey: secret.CubeKey})
	if err != nil {
		t.Fatal("Cube client unavailable")
	}
	s.CubeProxyURL = "http://127.0.0.1:80"
	s.CubeDomain = "cube.app"
	s.CubeAgentRelayOrigin = "https://functional.invalid"
	s.AgentProxyURL = "http://127.0.0.1:18291"
	s.CubeTemplates = map[string]string{"react-vite": "tpl-ce9efc43b71248d9a0adfb90"}
	s.CubeApps = map[string]bool{fixture.Project: true}
	app := &store.App{ID: fixture.Project, OwnerToken: cfgTenant, Name: "Synthetic Cube Claude functional", ExternalUserID: sql.NullString{String: fmt.Sprintf("baarcha:%d", fixture.Owner), Valid: true}}
	if err = s.Store.CreateApp(ctx, app); err != nil {
		t.Fatal("fixture app persistence failed")
	}
	report := map[string]any{"production_startup_accepted": false, "network_isolation_accepted": false, "template": "tpl-ce9efc43b71248d9a0adfb90"}
	t.Cleanup(func() {
		current, e := s.Store.CurrentSandboxForApp(context.Background(), fixture.Project)
		deleted := e != nil
		if e == nil {
			s.stopCubeEgress(current.ID)
			response := cubeRequest(s, "DELETE", "/v1/sandboxes/"+current.ID, "", cfgTenant)
			deleted = response.Code == 204
			if !deleted {
				t.Errorf("functional VM cleanup HTTP %d", response.Code)
			}
		}
		report["vm_deleted"] = deleted
		encoded, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile("/root/cube-claude-acceptance/report.json", encoded, 0600)
	})
	if err = s.ConfigureCubeEgress(ctx, CubeEgressConfig{Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}, BridgeURL: "https://functional.invalid/api/bridge"}); err != nil {
		t.Fatal("reverse fixture configuration failed")
	}
	// The private service runs through an authenticated SSH tunnel; only this
	// test substitutes its loopback destination, as existing reverse fixtures do.
	s.cubeEgress.config.BridgeURL = "http://127.0.0.1:18291/api/bridge"
	started := time.Now()
	created := cubeRequest(s, "POST", "/v1/apps/"+fixture.Project+"/sandbox", `{"runtime_preset":"react-vite"}`, cfgTenant)
	if created.Code != 201 {
		t.Fatalf("functional Cube create HTTP %d", created.Code)
	}
	report["create_ms"] = time.Since(started).Milliseconds()
	var sb sandboxResp
	if json.Unmarshal(created.Body.Bytes(), &sb) != nil || sb.ID == "" {
		t.Fatal("create returned no stable ID")
	}
	node := `node --input-type=module -e 'import fs from "node:fs";const send=async(body)=>{const r=await fetch(process.env.BRIDGE_URL,{method:"POST",headers:{"content-type":"application/json",authorization:"Bearer "+process.env.BRIDGE_TOKEN},body:JSON.stringify(body)});if(!r.ok)throw Error("bridge status "+r.status);return r.json()};const library=await send({kind:"library"});const item=library.files.find(f=>f.id==="` + fixture.FileID + `");if(!item)throw Error("missing fixture");const file=await send({kind:"file",id:item.id,offset:0});if(Buffer.from(file.data_b64,"base64").toString()!=="cube-functional-private-file\n")throw Error("wrong bytes");fs.writeFileSync("cube-smoke.txt","ready\n");console.log("functional bridge and file passed")'`
	prompt := "This is a bounded infrastructure fixture, not an app build. Do not inspect the repository or edit existing files. Execute exactly this one Bash command, then reply 'ready' and stop. Never print environment variables or credentials. No other tools, files, package installs, or tests are needed:\n" + node
	submit := func(prompt string) string {
		payload, _ := json.Marshal(map[string]any{"prompt": prompt, "agent": "claude-code", "model": "claude-sonnet-4-6", "timeout_s": 120, "continue": false, "env": map[string]string{"BRIDGE_TOKEN": fixture.BridgeToken, "BRIDGE_PROJECT": fixture.Project, "BRIDGE_URL": "https://functional.invalid/api/bridge", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "1024", "MAX_THINKING_TOKENS": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}})
		response := cubeRequest(s, "POST", "/v1/sandboxes/"+sb.ID+"/tasks", string(payload), cfgTenant)
		if response.Code != 202 && response.Code != 201 {
			t.Fatalf("task submit HTTP %d", response.Code)
		}
		var task struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(response.Body.Bytes(), &task) != nil || task.ID == "" {
			t.Fatal("task returned no ID")
		}
		return task.ID
	}
	wait := func(id string) *store.Task {
		for ctx.Err() == nil {
			task, e := s.Store.GetTask(ctx, id)
			if e == nil && (task.Status == "succeeded" || task.Status == "failed" || task.Status == "cancelled") {
				return task
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("task completion deadline")
		return nil
	}
	taskID := submit(prompt)
	task := wait(taskID)
	report["task_status"] = task.Status
	var outcome map[string]any
	if json.Unmarshal([]byte(task.ResultJSON.String), &outcome) == nil {
		for _, key := range []string{"failure_reason", "error_message", "build_error_message"} {
			if value, ok := outcome[key].(string); ok {
				value = strings.ReplaceAll(strings.ReplaceAll(value, fixture.BridgeToken, "[redacted]"), secret.CubeKey, "[redacted]")
				if len(value) > 2048 {
					value = value[:2048]
				}
				report[key] = value
			}
		}
	}
	if task.Status != "succeeded" {
		t.Fatalf("functional task ended %s", task.Status)
	}
	content, err := s.runtimeClientFor(sb.ID).ReadFile(ctx, "cube-smoke.txt")
	if err != nil || string(content) != "ready\n" {
		t.Fatal("synthetic task output missing")
	}
	report["synthetic_file_verified"] = true
	scope, err := s.Store.CubeModelScopeFor(ctx, taskID, sb.ID)
	_ = scope
	if err == nil {
		t.Fatal("completed task scope remained active")
	}
	report["completion_scope_revoked"] = true
	capability, err := s.cubeModelToken(ctx, sb.ID)
	if err != nil {
		t.Fatal("cannot derive test capability")
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/cube-model/"+sb.ID+"/"+taskID+"/v1/messages", strings.NewReader(`{}`))
	r.Header.Set("x-api-key", capability)
	r.Header.Set("x-baarcha-bridge", fixture.BridgeToken)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("completed relay HTTP %d", w.Code)
	}
	report["completed_relay_denied"] = true
	cancelID := submit("Infrastructure cancellation fixture. Execute only Bash 'sleep 60'. Do not inspect or change any files. Do not print credentials.")
	time.Sleep(2 * time.Second)
	response := cubeRequest(s, "POST", "/v1/sandboxes/"+sb.ID+"/tasks/"+cancelID+"/cancel", "", cfgTenant)
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("cancel HTTP %d", response.Code)
	}
	terminal := wait(cancelID)
	report["cancel_status"] = terminal.Status
	if terminal.Status != "cancelled" {
		t.Fatal("cancellation did not settle")
	}
	if _, err = s.Store.CubeModelScopeFor(ctx, cancelID, sb.ID); err == nil {
		t.Fatal("cancelled scope remained active")
	}
	report["cancellation_scope_revoked"] = true
	t.Log("PASS real Cube Claude task, synthetic file, completed relay denial, cancellation and scope revocation; check host report for actual metering and bridge counts")
}
