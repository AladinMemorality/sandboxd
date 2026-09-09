package api

import (
	"context"
	"database/sql"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskMessageOwnerAndTaskScope(t *testing.T) {
	s, app := newSnapTestServer(t)
	sb := &store.Sandbox{ID: newULID(), Status: "running", WorkspaceMnt: t.TempDir(), AppID: sql.NullString{String: app.ID, Valid: true}}
	if err := s.Store.Create(context.Background(), sb); err != nil {
		t.Fatal(err)
	}
	taskID := newULID()
	if err := s.Store.CreateTask(context.Background(), &store.Task{TaskID: taskID, SandboxID: sb.ID, Agent: "claude-code", Prompt: "initial"}); err != nil {
		t.Fatal(err)
	}
	const body = `{"message_id":"00000000-0000-4000-8000-000000000002","prompt":"Use blue"}`
	for _, c := range []struct {
		tenant, task, body string
		status             int
	}{
		{"another-tenant", taskID, body, 404},
		{cfgTenant, "wrong-task", body, 404},
		{cfgTenant, taskID, `{"message_id":"bad","prompt":"Use blue"}`, 400},
	} {
		r := reqAs("POST", "/v1/sandboxes/"+sb.ID+"/tasks/"+c.task+"/messages", c.body, c.tenant)
		r.SetPathValue("id", sb.ID)
		r.SetPathValue("taskId", c.task)
		w := httptest.NewRecorder()
		s.v1TaskMessage(w, r)
		if w.Code != c.status || strings.Contains(w.Body.String(), "Use blue") {
			t.Fatalf("got %d %s", w.Code, w.Body)
		}
	}
}
