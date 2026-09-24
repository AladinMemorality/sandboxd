package main

import (
	"context"
	"encoding/json"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateHistoryAndWorkspacePreserveCheckpointRevert(t *testing.T) {
	const id = "01M2QTJAM3F62CJ6ZQMEPE8H22"
	src := t.TempDir()
	appDir := filepath.Join(src, "app")
	os.Mkdir(appDir, 0755)
	os.WriteFile(filepath.Join(appDir, "main.js"), []byte("before task"), 0644)
	cp, e := checkpoint(appDir, id)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(appDir, "main.js"), []byte("after task"), 0644)
	task, e := newTask(runtime.StartTaskRequest{TaskID: id, Agent: "opencode"}, filepath.Join(src, "runtime/tasks"))
	if e != nil {
		t.Fatal(e)
	}
	task.finish(runtime.TaskResult{ID: id, Status: runtime.TaskSucceeded, CheckpointID: cp})
	work, e := runtime.ExportPrivateWorkspace(appDir)
	if e != nil {
		t.Fatal(e)
	}
	history, e := runtime.ExportPrivateTaskHistory(context.Background(), filepath.Join(src, "runtime/tasks"), []string{id})
	if e != nil {
		t.Fatal(e)
	}
	dst := t.TempDir()
	destApp := filepath.Join(dst, "app")
	os.Mkdir(destApp, 0755)
	destRuntime := filepath.Join(dst, "runtime")
	os.Mkdir(destRuntime, 0755)
	if e = runtime.InstallPrivateWorkspace(destApp, work); e != nil {
		t.Fatal(e)
	}
	if e = runtime.InstallPrivateTaskHistory(filepath.Join(destRuntime, "tasks"), history); e != nil {
		t.Fatal(e)
	}
	a := &app{appDir: destApp, runtimeDir: destRuntime, log: slog.Default()}
	req := httptest.NewRequest("POST", "/tasks/"+id+"/revert", nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	a.handleRevertTask(w, req)
	if w.Code != 200 {
		t.Fatalf("revert %d %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(filepath.Join(destApp, "main.js"))
	if string(b) != "before task" {
		t.Fatalf("checkpoint not restored %q", b)
	}
	events := httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/tasks/"+id+"/events", nil)
	req.SetPathValue("id", id)
	a.handleTaskEvents(events, req)
	if events.Code != 200 || !strings.Contains(events.Body.String(), "done") {
		t.Fatal("imported event history unavailable")
	}
	result, e := os.ReadFile(filepath.Join(destRuntime, "tasks", id, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var parsed runtime.TaskResult
	if json.Unmarshal(result, &parsed) != nil || parsed.CheckpointID != cp {
		t.Fatal("checkpoint result metadata lost")
	}
}
func TestPrivateHistoryRoutesRequireQuiescenceAndAuthentication(t *testing.T) {
	a := &app{}
	token := strings.Repeat("ab", 32)
	handler := authenticatedControl(token, a.controlHandler())
	for _, path := range []string{"/export/private-task-history", "/import/private-task-history"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{"task_ids":[]}`)))
		if w.Code != 401 {
			t.Fatal("unauthenticated private history accepted")
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"task_ids":[]}`))
		if strings.Contains(path, "/import/") {
			req.Method = "PUT"
		}
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 409 {
			t.Fatalf("unquiesced history status %d", w.Code)
		}
	}
}
func TestPrivateTaskHistoryGuestTransportRoundtrip(t *testing.T) {
	if os.Geteuid() != 1000 || os.Getenv("RUNTIMED_CUBE_GUEST") != "1" {
		t.Skip("requires isolated Cube UID1000 fixture")
	}
	const id = "01M2QTJAM3F62CJ6ZQMEPE8H22"
	base, e := os.MkdirTemp("/home/sandbox/workspace", "task-history-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(base)
	appDir := filepath.Join(base, "app")
	os.Mkdir(appDir, 0755)
	runtimeDir := filepath.Join(base, "runtime")
	os.Mkdir(runtimeDir, 0755)
	task, e := newTask(runtime.StartTaskRequest{TaskID: id, Agent: "opencode"}, filepath.Join(runtimeDir, "tasks"))
	if e != nil {
		t.Fatal(e)
	}
	task.finish(runtime.TaskResult{ID: id, Status: runtime.TaskSucceeded, CheckpointID: "known-checkpoint"})
	a := &app{appDir: appDir, runtimeDir: runtimeDir}
	handler := a.controlHandler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := call("POST", "/workspace/quiesce", ""); w.Code != 200 {
		t.Fatalf("quiesce %d %s", w.Code, w.Body)
	}
	exported := call("POST", "/export/private-task-history", `{"task_ids":["`+id+`"]}`)
	if exported.Code != 200 {
		t.Fatalf("export %d %s", exported.Code, exported.Body)
	}
	os.RemoveAll(filepath.Join(runtimeDir, "tasks", id))
	if w := call("PUT", "/import/private-task-history", exported.Body.String()); w.Code != 200 {
		t.Fatalf("import %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/workspace/resume", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	result := call("GET", "/tasks/"+id+"/result", "")
	if result.Code != 200 || !strings.Contains(result.Body.String(), "known-checkpoint") {
		t.Fatalf("result %d %s", result.Code, result.Body)
	}
}
func TestImportedHistoryStartsFreshThenContinuesLocalTask(t *testing.T) {
	const oldID = "01M2QTJAM3F62CJ6ZQMEPE8H22"
	const newID = "01M2QTJAYD0JCM4665XT28G5PJ"
	src := t.TempDir()
	prior, e := newTask(runtime.StartTaskRequest{TaskID: oldID, Agent: "opencode"}, src)
	if e != nil {
		t.Fatal(e)
	}
	prior.finish(runtime.TaskResult{ID: oldID, Status: runtime.TaskSucceeded})
	archive, e := runtime.ExportPrivateTaskHistory(context.Background(), src, []string{oldID})
	if e != nil {
		t.Fatal(e)
	}
	// Merge back onto an existing identical task as rollback does: it must also
	// become history-only, since the retained provider session lacks Cube work.
	if e = runtime.InstallPrivateTaskHistory(src, archive); e != nil {
		t.Fatal(e)
	}
	if hasPriorTask(src, newID) {
		t.Fatal("imported task implied a provider session")
	}
	local, e := newTask(runtime.StartTaskRequest{TaskID: newID, Agent: "opencode"}, src)
	if e != nil {
		t.Fatal(e)
	}
	if local.cont {
		t.Fatal("first local task tried to resume absent session")
	}
	local.finish(runtime.TaskResult{ID: newID, Status: runtime.TaskSucceeded})
	if !hasPriorTask(src, "01M2QTJAYD0JCM4665XT28G5PK") {
		t.Fatal("new local completed task did not enable continuation")
	}
}

func TestInterruptedHistoryStagingIsNeverASessionOrTask(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, ".task-import-partial")
	complete := filepath.Join(root, ".task-import-complete")
	os.Mkdir(partial, 0755)
	os.Mkdir(complete, 0755)
	os.WriteFile(filepath.Join(partial, "events.jsonl"), []byte("{\"type\":\"status\"}\n"), 0644)
	os.WriteFile(filepath.Join(complete, "result.json"), []byte(`{"id":"01M2QTJAM3F62CJ6ZQMEPE8H22","status":"succeeded"}`), 0644)
	if hasPriorTask(root, "new-task") {
		t.Fatal("interrupted staging implied a provider session")
	}
	recoverInterruptedTasks(root, slog.Default())
	if _, e := os.Stat(filepath.Join(partial, "result.json")); !os.IsNotExist(e) {
		t.Fatal("interrupted staging recovered as a synthetic task")
	}
	a := &app{runtimeDir: filepath.Dir(root)}
	// The list handler expects <runtimeDir>/tasks; move this isolated fixture to
	// that exact layout without changing the contents under review.
	runtimeDir := t.TempDir()
	os.Rename(root, filepath.Join(runtimeDir, "tasks"))
	a.runtimeDir = runtimeDir
	w := httptest.NewRecorder()
	a.handleListTasks(w, httptest.NewRequest("GET", "/tasks", nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"tasks":[]}` {
		t.Fatalf("staging appeared in task list: %d %s", w.Code, w.Body)
	}
}
