package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/audit"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/events"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/manifest"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/wake"
)

func (s *Server) recreateDockerSandbox(w http.ResponseWriter, r *http.Request, id string) {
	reload, valid := readRecreateRequest(w, r)
	if !valid {
		return
	}
	if s.Locks != nil {
		s.Locks.Lock(id)
		defer s.Locks.Unlock(id)
		r = r.WithContext(wake.WithLifecycleLockHeld(r.Context(), id))
	}
	sb, err := s.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return
	}
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot resolve sandbox")
		return
	}
	if sb.AppID.Valid {
		if _, err := s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r)); err != nil {
			writeV1Err(w, 404, "not_found", "no such sandbox")
			return
		}
	}
	if sb.Status != "running" && sb.Status != "stopped" {
		writeV1Err(w, 409, "conflict", "sandbox is "+sb.Status+" — cannot recreate")
		return
	}
	active, err := s.Store.SandboxHasRunningTask(r.Context(), id)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot resolve active tasks")
		return
	}
	if active {
		writeV1Err(w, 409, "task_in_progress", "a task is in progress; apply changes after it finishes")
		return
	}
	var digest, revision, imageID string
	if reload {
		raw, present, err := runtime.ReadAppManifest(s.appDirFor(id))
		if err != nil {
			writeV1Err(w, 422, "invalid_manifest", "manifest must be a bounded regular workspace file")
			return
		}
		if present {
			validation := manifest.Validate(raw)
			if !validation.Valid || validation.Effective == nil || (validation.Effective.Web != nil && validation.Effective.Web.Port != webPortOf(sb)) {
				writeV1Err(w, 422, "invalid_manifest", "manifest must be valid and retain the exposed web port")
				return
			}
		} else if webPortOf(sb) != manifest.DefaultWebPort {
			writeV1Err(w, 422, "invalid_manifest", "default manifest would change the exposed web port")
			return
		}
		environment, err := appenv.For(r.Context(), s.Store, s.Secrets, sb.AppID.String)
		if err != nil {
			writeV1Err(w, 503, "runtime_unavailable", "runtime configuration unavailable")
			return
		}
		digest, revision = runtime.ManifestSourceDigest(raw, present), runtime.DockerConfigRevision(environment)
		imageID, err = s.Docker.ImageID(r.Context(), s.Image)
		if err != nil {
			writeV1Err(w, 503, "runtime_unavailable", "runtime image identity unavailable")
			return
		}
	}
	current, inspectErr := s.Docker.Inspect(r.Context(), "s-"+id)
	if inspectErr != nil && !errors.Is(inspectErr, docker.ErrNotFound) {
		writeV1Err(w, 503, "runtime_unavailable", "container inspection unavailable")
		return
	}
	running := inspectErr == nil && current.State.Running
	if running || sb.Status == "running" {
		statusCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		status, statusErr := s.runtimeClientFor(id).Status(statusCtx)
		cancel()
		if statusErr == nil && status.ActiveTask != nil {
			writeV1Err(w, 409, "task_in_progress", "a task is in progress; apply changes after it finishes")
			return
		}
		if reload && running && statusErr != nil {
			writeV1Err(w, 503, "runtime_unavailable", "supervisor unavailable before manifest activation")
			return
		}
		if reload && running && sb.Status == "running" && sb.ContainerID.Valid && sb.ContainerID.String == current.ID && statusErr == nil && current.Config.Image == s.Image && current.Image == imageID && status.ManifestSHA256 == digest && status.AppConfigRevision == revision {
			writeJSON(w, 200, s.v1SandboxFromRow(r, sb))
			return
		}
		if running {
			if err := s.Docker.Stop(r.Context(), "s-"+id, 10); err != nil {
				writeV1Err(w, 500, "internal", "docker stop failed")
				return
			}
		}
		if err := s.Store.MarkStoppedAt(r.Context(), id, time.Now().UTC()); err != nil {
			writeV1Err(w, 503, "runtime_unavailable", "cannot persist stopped state")
			return
		}
	}
	if err := s.Docker.Remove(r.Context(), "s-"+id); err != nil && !errors.Is(err, docker.ErrNotFound) {
		writeV1Err(w, 500, "internal", "docker remove failed")
		return
	}
	s.auditAction(r, audit.Entry{Action: "sandbox.recreate", Target: id})
	code, body := s.delegate(r, s.handleWakeJSON, http.MethodPost, "/wake/"+id, map[string]string{"id": id}, nil)
	if code != http.StatusOK {
		relayV1Error(w, code, body)
		return
	}
	if reload {
		// A container start alone is not acknowledgement. The live supervisor must
		// report exactly the manifest it parsed and the app environment it received.
		ackCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		for {
			status, statusErr := s.runtimeClientFor(id).Status(ackCtx)
			current, inspectErr = s.Docker.Inspect(ackCtx, "s-"+id)
			if statusErr == nil && inspectErr == nil && current.State.Running && current.Config.Image == s.Image && current.Image == imageID && status.ManifestSHA256 == digest && status.AppConfigRevision == revision {
				break
			}
			select {
			case <-ackCtx.Done():
				writeV1Err(w, 502, "runtime_unavailable", "manifest acknowledgement pending")
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	sb, err = s.Store.Get(r.Context(), id)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot read sandbox")
		return
	}
	s.recordEvent(r, events.Event{Type: events.SandboxStarted, Severity: events.SeverityInfo, Message: "Sandbox recreated", AppID: sb.AppID.String, SandboxID: id})
	writeJSON(w, 200, s.v1SandboxFromRow(r, sb))
}
