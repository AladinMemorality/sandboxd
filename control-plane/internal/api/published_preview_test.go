package api

import (
	"archive/zip"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/activity"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/publication"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestPublishedCopyLetsNextEditStartAndRejectsStalePromotion(t *testing.T) {
	copying, release := make(chan struct{}), make(chan struct{})
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files" {
			w.Write([]byte(`{"entries":[{"path":"dist/index.html","type":"file","size":5}]}`))
		} else if r.URL.Query().Get("path") == "package.json" {
			close(copying)
			<-release
			w.Write([]byte(`{"devDependencies":{"vite":"1"}}`))
		} else {
			w.Write([]byte("built"))
		}
	})
	s.PublishedRoot = t.TempDir()
	s.Inflight = activity.NewInflightExec()
	ctx := context.Background()
	if err := s.Store.MarkRunning(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	}
	old, next := "01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	if err := s.Store.CreateTask(ctx, &store.Task{TaskID: old, SandboxID: id, Agent: "opencode", Prompt: "build"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.FinishTask(ctx, old, "succeeded", `{"build_status":"passed"}`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.capturePublishedTask(id, old) }()
	select {
	case <-copying:
	case err := <-done:
		t.Fatalf("copy did not start: %v", err)
	}
	edit := make(chan error, 1)
	go func() {
		s.Locks.Lock(id)
		defer s.Locks.Unlock(id)
		edit <- s.Store.CreateTask(ctx, &store.Task{TaskID: next, SandboxID: id, Agent: "opencode", Prompt: "next edit"})
	}()
	select {
	case err := <-edit:
		if err != nil {
			close(release)
			<-done
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("remote build transfer blocks next edit")
	}
	if !s.Inflight.Active(id) {
		t.Error("copy can be idled midway")
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale build accepted")
	}
	if _, err := publication.Current(s.PublishedRoot, id); err == nil {
		t.Fatal("stale build became visible")
	}
	if s.Inflight.Active(id) {
		t.Fatal("copy left guest pinned")
	}
}

func TestPublishedBackendOriginRetainsConfiguredAppContract(t *testing.T) {
	s, _, _ := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://configured.preview.test" {
			t.Error("backend origin contract changed")
		}
		w.Write([]byte("saved"))
	})
	sb, _ := s.Store.Get(context.Background(), cubePreviewTestID)
	err := s.Store.CreateAppConfig(context.Background(), &store.AppConfig{ID: "origin-config", AppID: sb.AppID.String, Key: "APP_ORIGIN", ValuePlaintext: sql.NullString{String: "https://configured.preview.test", Valid: true}, AccessPolicy: "runtime_allowed"})
	if err != nil {
		t.Fatal(err)
	}
	r := cubePreviewRequest(t, "POST", "/api/save", "payload")
	r.Host = strings.Replace(r.Host, "s-", "p-", 1)
	r.Header.Set("Origin", "https://"+r.Host)
	w := httptest.NewRecorder()
	s.TryServeCubePreview(w, r)
	if w.Code != 200 {
		t.Fatalf("backend %d", w.Code)
	}
	r.Header.Set("Origin", "https://another-app.test")
	w = httptest.NewRecorder()
	s.TryServeCubePreview(w, r)
	if w.Code != 403 {
		t.Fatal("sibling origin translated")
	}
}

func TestPublishedFrontendUsesSameACLButNeverWakesEditorGuest(t *testing.T) {
	s, connects, guestCalls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("live development")) })
	s.PublishedRoot = t.TempDir()
	dir := filepath.Join(s.PublishedRoot, cubePreviewTestID)
	os.MkdirAll(dir, 0700)
	revision := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	f, err := os.Create(filepath.Join(dir, revision+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	entry, _ := z.Create("index.html")
	entry.Write([]byte("production frontend"))
	z.Close()
	f.Close()
	os.WriteFile(filepath.Join(dir, "current"), []byte(revision), 0600)
	request := func(cookie bool) *http.Request {
		r := cubePreviewRequest(t, "GET", "/", "")
		r.Host = strings.Replace(r.Host, "s-", "p-", 1)
		if !cookie {
			r.Header.Del("Cookie")
		}
		return r
	}
	w := httptest.NewRecorder()
	s.TryServeCubePreview(w, request(false))
	if w.Code == 200 {
		t.Fatal("private build exposed")
	}
	w = httptest.NewRecorder()
	s.TryServeCubePreview(w, request(true))
	if w.Code != 200 || w.Body.String() != "production frontend" {
		t.Fatalf("build %d %s", w.Code, w.Body.String())
	}
	if connects.Load() != 0 || guestCalls.Load() != 0 {
		t.Fatal("static frontend woke guest")
	}
	r := request(true)
	r.Header.Set("Origin", "https://p-another-3000.preview.example.test")
	w = httptest.NewRecorder()
	s.TryServeCubePreview(w, r)
	if w.Code != 403 {
		t.Fatal("cross-app origin accepted")
	}
	w = httptest.NewRecorder()
	s.TryServeCubePreview(w, cubePreviewRequest(t, "GET", "/", ""))
	if w.Code != 200 || w.Body.String() != "live development" || guestCalls.Load() != 1 {
		t.Fatalf("editor no longer live: %d %s", w.Code, w.Body.String())
	}
	sb, _ := s.Store.Get(context.Background(), cubePreviewTestID)
	r = httptest.NewRequest("POST", "/published-preview", nil)
	r.SetPathValue("id", sb.AppID.String)
	r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: "wrong-tenant", Kind: "service"}))
	w = httptest.NewRecorder()
	s.v1PublishedPreview(w, r)
	if w.Code != 404 {
		t.Fatal("cross-tenant build access minted")
	}
	r = r.WithContext(auth.WithActor(r.Context(), auth.Actor{Name: cfgTenant, Kind: "service"}))
	w = httptest.NewRecorder()
	s.v1PublishedPreview(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://p-") {
		t.Fatalf("build access %d %s", w.Code, w.Body.String())
	}
}
