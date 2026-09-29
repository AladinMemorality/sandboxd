package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/oklog/ulid/v2"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/publication"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

var publicationSlots = make(chan struct{}, 2)

// Translate only a verified same-origin visitor request to the backend's
// existing origin contract. Sibling origins never acquire this authority.
func (s *Server) publishedBackendOrigin(r *http.Request, sb *store.Sandbox) string {
	scheme, _ := s.previewScheme()
	if !strings.HasPrefix(strings.ToLower(r.Host), "p-") || !strings.EqualFold(r.Header.Get("Origin"), scheme+"://"+r.Host) {
		return ""
	}
	origin := scheme + "://s-" + r.Host[2:]
	if sb.AppID.Valid {
		cfg, err := s.Store.GetAppConfig(r.Context(), sb.AppID.String, "APP_ORIGIN")
		if err == nil {
			origin = cfg.ValuePlaintext.String
			if cfg.Sensitive {
				if s.Secrets == nil {
					return ""
				}
				value, e := s.Secrets.Open(cfg.ValueCiphertext, cfg.ValueNonce)
				if e != nil {
					return ""
				}
				origin = string(value)
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return ""
		}
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return origin
}

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
	target, err := url.Parse(s.previewURL(sb.ID, webPortOf(sb)))
	if err != nil || !strings.HasPrefix(target.Host, "s-") {
		writeV1Err(w, 503, "preview_unavailable", "preview origin unavailable")
		return
	}
	target.Host = "p-" + strings.TrimPrefix(target.Host, "s-")
	if sb.Visibility == "public" {
		// Public preview requests already require no cookie. Do not make every
		// visitor pay for a redundant capability handoff and redirect.
		writeJSON(w, 200, map[string]any{"url": target.String(), "access_url": target.String(), "public": true})
		return
	}
	owner, err := s.Store.GetWorkspaceOwner(r.Context(), sb.ID)
	if err != nil || owner.ExternalUserID == "" {
		writeV1Err(w, 409, "preview_owner_unavailable", "preview owner unavailable")
		return
	}
	s.writeCubePreviewAccess(w, sb.ID, owner.ExternalUserID, target.String())
}

// Explicit backfill uses the same collector as normal task completion. It does
// not start guests or run arbitrary project code on the controller.
func (s *Server) v1PublishedBuild(w http.ResponseWriter, r *http.Request) {
	sb := s.publishedSandbox(w, r)
	if sb == nil {
		return
	}
	if r.Header.Get("Content-Type") == "application/zip" {
		select {
		case publicationSlots <- struct{}{}:
			defer func() { <-publicationSlots }()
		default:
			writeV1Err(w, 503, "build_busy", "publisher busy")
			return
		}
		// Trusted project publishers may supply a build made by their CI, for
		// imported projects that have no AI task history. Same atomic collector.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
		if err != nil {
			writeV1Err(w, 413, "invalid_build", "build exceeds limit")
			return
		}
		source, err := publication.Bundle(body)
		if err != nil {
			writeV1Err(w, 400, "invalid_build", "invalid production bundle")
			return
		}
		if s.Locks != nil {
			s.Locks.Lock(sb.ID)
			defer s.Locks.Unlock(sb.ID)
		}
		active, err := s.Store.SandboxHasRunningTask(r.Context(), sb.ID)
		if err != nil || active {
			writeV1Err(w, 409, "build_busy", "project is being edited")
			return
		}
		err = publication.Capture(r.Context(), s.PublishedRoot, sb.ID, ulid.Make().String(), source, nil)
		if err != nil {
			writeV1Err(w, 400, "invalid_build", "no complete supported production build")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "published"})
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
	// Keep the guest alive for the copy without making the next AI edit wait
	// for a cross-worker transfer. The final commit re-checks task generation.
	if s.Locks != nil {
		s.Locks.Lock(id)
	}
	if s.Inflight != nil {
		s.Inflight.Enter(id)
		defer s.Inflight.Exit(id)
	}
	if s.Locks != nil {
		s.Locks.Unlock(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	locked := false
	defer func() {
		if locked {
			s.Locks.Unlock(id)
		}
	}()
	err = publication.Capture(ctx, s.PublishedRoot, id, taskID, s.runtimeClientFor(id), func() error {
		if s.Locks != nil {
			s.Locks.Lock(id)
			locked = true
		}
		if _, e := s.Store.Get(ctx, id); e != nil {
			return e
		}
		latest, e := s.Store.ListTasksForSandbox(ctx, id, 1)
		if e != nil {
			return e
		}
		if len(latest) != 1 || latest[0].TaskID != taskID || latest[0].Status == "running" {
			return errors.New("project changed during build capture")
		}
		return nil
	})
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
