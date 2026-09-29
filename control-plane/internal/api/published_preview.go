package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/publication"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

var publicationSlots = make(chan struct{}, 2)

func (s *Server) publishedSandbox(w http.ResponseWriter, r *http.Request) *store.Sandbox {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := r.PathValue("id")
	if s.PublishedRoot == "" {
		writeV1Err(w, 404, "not_found", "no published build")
		return nil
	}
	if _, err := s.Store.GetAppForOwner(r.Context(), id, tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such app")
		return nil
	}
	sb, err := s.Store.CurrentSandboxForApp(r.Context(), id)
	if err != nil || sb.RuntimeProvider != "cube" {
		writeV1Err(w, 404, "not_found", "no published build")
		return nil
	}
	return sb
}

// This reads local state only: serving a frontend must not wake a sandbox.
func (s *Server) v1PublishedPreview(w http.ResponseWriter, r *http.Request) {
	sb := s.publishedSandbox(w, r)
	if sb == nil {
		return
	}
	if _, err := publication.Current(s.PublishedRoot, sb.ID); err != nil {
		writeV1Err(w, 404, "not_found", "no published build")
		return
	}
	owner, err := s.Store.GetWorkspaceOwner(r.Context(), sb.ID)
	if err != nil || owner.ExternalUserID == "" {
		writeV1Err(w, 409, "preview_owner_unavailable", "preview owner unavailable")
		return
	}
	target, err := url.Parse(s.previewURL(sb.ID, webPortOf(sb)))
	if err != nil || !strings.HasPrefix(target.Host, "s-") {
		writeV1Err(w, 503, "preview_unavailable", "preview origin unavailable")
		return
	}
	target.Host = "p-" + strings.TrimPrefix(target.Host, "s-")
	s.writeCubePreviewAccess(w, sb.ID, owner.ExternalUserID, target.String())
}

// Explicit backfill uses the same collector as normal task completion. It does
// not start guests or run arbitrary project code on the controller.
func (s *Server) v1PublishedBuild(w http.ResponseWriter, r *http.Request) {
	sb := s.publishedSandbox(w, r)
	if sb == nil {
		return
	}
	tasks, err := s.Store.ListTasksForSandbox(r.Context(), sb.ID, 1)
	if err != nil || len(tasks) != 1 {
		writeV1Err(w, 409, "build_unavailable", "no completed production build")
		return
	}
	if err = s.capturePublishedTask(sb.ID, tasks[0].TaskID); err != nil {
		writeV1Err(w, 409, "build_unavailable", "no complete supported production build")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "published"})
}

func (s *Server) capturePublishedTask(id, taskID string) error {
	select {
	case publicationSlots <- struct{}{}:
		defer func() { <-publicationSlots }()
	default:
		return errors.New("publisher busy")
	}
	// All task submission and platform file mutations take this same lock.
	// A newer edit wins; never publish its partially written build as an old one.
	if s.Locks != nil {
		s.Locks.Lock(id)
		defer s.Locks.Unlock(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sb, err := s.Store.Get(ctx, id)
	if err != nil || sb.RuntimeProvider != "cube" || sb.Status != "running" {
		return publication.ErrUnsupported
	}
	tasks, err := s.Store.ListTasksForSandbox(ctx, id, 1)
	if err != nil {
		return err
	}
	if len(tasks) != 1 || tasks[0].TaskID != taskID || tasks[0].Status == "running" {
		return errors.New("newer or active task")
	}
	var result runtime.TaskResult
	if json.Unmarshal([]byte(tasks[0].ResultJSON.String), &result) != nil || result.BuildStatus != runtime.BuildPassed {
		return publication.ErrUnsupported
	}
	err = publication.Capture(ctx, s.PublishedRoot, id, taskID, s.runtimeClientFor(id))
	if err != nil && s.Log != nil {
		s.Log.Info("production frontend capture deferred", "sandbox", id, "task", taskID)
	}
	return err
}

// Retry interrupted/failed copies from durable task results, including after a
// controller restart. Sleeping guests stay asleep; the last build stays live.
func (s *Server) RunPublishedBuilds(ctx context.Context) {
	if s.PublishedRoot == "" {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		sandboxes, err := s.Store.List(ctx)
		if err != nil {
			continue
		}
		for _, sb := range sandboxes {
			if ctx.Err() != nil {
				return
			}
			if sb.RuntimeProvider != "cube" || sb.Status != "running" {
				continue
			}
			tasks, err := s.Store.ListTasksForSandbox(ctx, sb.ID, 1)
			if err != nil || len(tasks) != 1 {
				continue
			}
			current, _ := publication.Current(s.PublishedRoot, sb.ID)
			if current >= tasks[0].TaskID {
				continue
			}
			_ = s.capturePublishedTask(sb.ID, tasks[0].TaskID)
		}
	}
}
