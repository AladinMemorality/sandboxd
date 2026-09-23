package api

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/loopback"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestRetainedDockerHistoryPreservesOwnerAndResumeCursor(t *testing.T) {
	s, appID := newConfigTestServer(t)
	ctx := context.Background()
	id := newULID()
	taskID := newULID()
	s.Loopback = loopback.New()
	s.Loopback.Root = t.TempDir()
	if err := s.Store.Create(ctx, &store.Sandbox{ID: id, Status: "stopped", AppID: sql.NullString{String: appID, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.CreateTask(ctx, &store.Task{TaskID: taskID, SandboxID: id, Agent: "claude-code", Prompt: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.FinishTask(ctx, taskID, "succeeded", `{"checkpoint_id":"original-checkpoint"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.BeginRuntimeMigration(ctx, id, "react-vite", "trusted", "cube.test"); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{{"planned", "quiesced"}, {"quiesced", "archived"}} {
		if err := s.Store.AdvanceRuntimeMigration(ctx, id, step[0], step[1], "digest"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.PrepareMigrationTargetCredential(ctx, id, []byte("cipher"), []byte("nonce")); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveMigrationTarget(ctx, id, &store.RuntimeBinding{RuntimeID: "remote", TokenCiphertext: []byte("cipher"), TokenNonce: []byte("nonce")}); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{{"staged", "imported"}, {"imported", "verified"}} {
		if err := s.Store.AdvanceRuntimeMigration(ctx, id, step[0], step[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.CommitRuntimeMigration(ctx, id); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Loopback.Root, id, ".runtimed", "tasks", taskID, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"id\":0,\"type\":\"delta\",\"data\":{\"text\":\"private old event\"}}\n{\"id\":1,\"type\":\"done\",\"data\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner, cursor string
		code          int
	}{{cfgTenant, "", 200}, {cfgTenant, "0", 200}, {"different-owner", "", 404}} {
		request := httptest.NewRequest("GET", "/v1/sandboxes/"+id+"/tasks/"+taskID+"/events", nil)
		request = request.WithContext(auth.WithActor(ctx, auth.Actor{Name: tc.owner, Kind: "service"}))
		request.Header.Set("Last-Event-ID", tc.cursor)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.owner, tc.cursor, response.Code, response.Body.String())
		}
		if tc.owner != cfgTenant && strings.Contains(response.Body.String(), "private old event") {
			t.Fatal("cross-owner history leak")
		}
		if tc.cursor != "" && strings.Contains(response.Body.String(), "private old event") {
			t.Fatal("resume cursor ignored")
		}
		if tc.code == 200 && !strings.Contains(response.Body.String(), "event: done") {
			t.Fatal("historical completion not replayed")
		}
	}
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/v1/sandboxes/" + id + "/tasks", 200}, {"POST", "/v1/sandboxes/" + id + "/tasks/" + taskID + "/revert", 409}} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request = request.WithContext(auth.WithActor(ctx, auth.Actor{Name: cfgTenant, Kind: "service"}))
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != tc.code {
			t.Fatalf("historical action %s: %d %s", tc.path, response.Code, response.Body)
		}
		if tc.method == "GET" && (!strings.Contains(response.Body.String(), `"can_revert":false`) || !strings.Contains(response.Body.String(), `"revert_unavailable_reason"`)) {
			t.Fatal("UI advertised unavailable historical revert", response.Body)
		}
	}
	// The file is tenant writable while Docker runs: links must never disclose
	// a host file, even though the source is normally stopped after migration.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "sentinel")
	os.WriteFile(outside, []byte("host secret"), 0600)
	for _, link := range []func(string, string) error{os.Symlink, os.Link} {
		if err := link(outside, path); err != nil {
			t.Fatal(err)
		}
		if f, err := openRetainedHistory(filepath.Join(s.Loopback.Root, id), taskID); err == nil {
			f.Close()
			t.Fatal("host link accepted")
		}
		os.Remove(path)
	}
}
