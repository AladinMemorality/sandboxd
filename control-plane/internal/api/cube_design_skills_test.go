package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCubeDoesNotAcceptTaskWithoutDesignPack(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, nil)
	var submissions atomic.Int32
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{}`))
		case "/files/content":
			w.WriteHeader(404)
		case "/files":
			w.WriteHeader(403)
		case "/tasks":
			submissions.Add(1)
			w.WriteHeader(202)
		default:
			w.WriteHeader(404)
		}
	}))
	defer guest.Close()
	s.CubeProxyURL = guest.URL
	w := cubeRequest(s, "POST", "/v1/sandboxes/"+id+"/tasks", `{"prompt":"build a site","agent":"opencode"}`, cfgTenant)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "design_skills_unavailable") {
		t.Fatalf("unexpected rejection: %d %s", w.Code, w.Body.String())
	}
	if submissions.Load() != 0 {
		t.Fatal("started agent without the pack")
	}
	active, err := s.Store.SandboxHasRunningTask(context.Background(), id)
	if err != nil || active {
		t.Fatalf("failed delivery left running task: %v %v", active, err)
	}
}
