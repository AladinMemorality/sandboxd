package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/loopback"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// The legacy runtime knows nothing about history-import markers. Its old
// continuation policy accepts explicit false, which the API must send even
// when the caller requests true after rollback.
func TestRollbackForcesFreshSessionAtLegacyRuntimeBoundary(t *testing.T) {
	s, appID := newConfigTestServer(t)
	ctx := context.Background()
	id := newULID()
	s.Loopback = loopback.New()
	shortRoot, err := os.MkdirTemp("", "rollback-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(shortRoot) })
	s.Loopback.Root = shortRoot
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Store.Create(ctx, &store.Sandbox{ID: id, Status: "stopped", AppID: sql.NullString{String: appID, Valid: true}}))
	must(s.Store.BeginRuntimeMigration(ctx, id, "react-vite", "trusted", "cube.test"))
	for _, step := range [][2]string{{"planned", "quiesced"}, {"quiesced", "archived"}} {
		must(s.Store.AdvanceRuntimeMigration(ctx, id, step[0], step[1], "digest"))
	}
	must(s.Store.PrepareMigrationTargetCredential(ctx, id, []byte("sealed"), []byte("nonce")))
	must(s.Store.SaveMigrationTarget(ctx, id, &store.RuntimeBinding{RuntimeID: "remote", TokenCiphertext: []byte("sealed"), TokenNonce: []byte("nonce")}))
	for _, step := range [][2]string{{"staged", "imported"}, {"imported", "verified"}} {
		must(s.Store.AdvanceRuntimeMigration(ctx, id, step[0], step[1], ""))
	}
	must(s.Store.CommitRuntimeMigration(ctx, id))
	for _, step := range [][2]string{{"complete", "rollback_started"}, {"rollback_started", "rollback_archived"}, {"rollback_archived", "rollback_restored"}} {
		must(s.Store.AdvanceRuntimeMigration(ctx, id, step[0], step[1], "rollback-digest"))
	}
	must(s.Store.CommitRuntimeRollback(ctx, id))
	must(s.Store.MarkRunning(ctx, id, "legacy-container", "cgroup"))
	_, mnt := s.Loopback.Paths(id)
	must(os.MkdirAll(filepath.Join(mnt, ".runtimed"), 0700))
	listener, err := net.Listen("unix", filepath.Join(mnt, ".runtimed/sock"))
	must(err)
	requests := make(chan runtime.StartTaskRequest, 4)
	legacy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request runtime.StartTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		requests <- request
		// Refusal models failure before launching a CLI session. It must not consume
		// the fresh-session requirement, and avoids starting a watcher in this test.
		http.Error(w, "busy", 409)
	})}
	go legacy.Serve(listener)
	defer legacy.Close()
	submit := func(want bool) {
		t.Helper()
		request := httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"prompt":"build","agent":"claude-code","continue":true}`))
		request.SetPathValue("id", id)
		response := httptest.NewRecorder()
		s.v1SubmitTask(response, request)
		if response.Code != 409 {
			t.Fatalf("submit: %d %s", response.Code, response.Body)
		}
		got := <-requests
		if got.Continue == nil || *got.Continue != want {
			t.Fatalf("legacy continuation = %v, want %v", got.Continue, want)
		}
	}
	submit(false)
	submit(false)
	must(s.Store.CreateTask(ctx, &store.Task{TaskID: newULID(), SandboxID: id, Agent: "claude-code", Prompt: "new local session"}))
	tasks, err := s.Store.ListTasksForSandbox(ctx, id, 1)
	must(err)
	must(s.Store.FinishTask(ctx, tasks[0].TaskID, "succeeded", "{}"))
	submit(true)
}
