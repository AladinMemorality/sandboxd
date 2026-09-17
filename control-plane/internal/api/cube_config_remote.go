package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

var errCubeConfigBusy = errors.New("runtime config waits for the active task")

// syncCubeAppConfig runs under the sandbox lifecycle lock. Only explicitly
// runtime-visible values leave the encrypted control-plane store. A full
// replacement removes deleted/revoked values instead of accumulating secrets.
func (s *Server) syncCubeAppConfig(ctx context.Context, id string) error {
	b, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		return err
	}
	if b.ConfigRevision == b.ConfigAppliedRevision {
		return nil
	}
	active, err := s.Store.SandboxHasRunningTask(ctx, id)
	if err != nil {
		return err
	}
	if active {
		return errCubeConfigBusy
	}
	sb, err := s.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !sb.AppID.Valid {
		return errors.New("Cube config owner unavailable")
	}
	entries, err := appenv.For(ctx, s.Store, s.Secrets, sb.AppID.String)
	if err != nil {
		return errors.New("runtime config could not be decrypted")
	}
	env := map[string]string{}
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	revision := id + ":" + strconv.FormatInt(b.ConfigRevision, 10)
	request := runtime.AppConfigRequest{Env: env, Revision: revision}
	if err := runtime.ValidateAppConfig(request); err != nil {
		return errors.New("runtime config contains an unsupported environment key or value")
	}
	client := s.runtimeClientFor(id)
	status, err := client.Status(ctx)
	if err != nil {
		return err
	}
	if status.ActiveTask != nil {
		return errCubeConfigBusy
	}
	if err := client.ApplyAppConfig(ctx, request); err != nil {
		return err
	}
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		status, err := client.Status(ready)
		if err == nil && status.AppConfigRevision == revision {
			return s.Store.MarkCubeConfigApplied(ready, id, b.ConfigRevision)
		}
		select {
		case <-ready.Done():
			return errors.New("runtime config acknowledgement pending")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Server) cubeConfigState(ctx context.Context, appID string) string {
	sb, err := s.Store.CurrentSandboxForApp(ctx, appID)
	if err != nil || sb.RuntimeProvider != "cube" {
		return ""
	}
	b, err := s.Store.GetRuntimeBinding(ctx, sb.ID)
	if err != nil {
		return "pending"
	}
	if b.ConfigRevision != b.ConfigAppliedRevision {
		return "pending"
	}
	return "applied"
}

// noteCubeConfigApply reports durable save separately from process application.
// Stopped VMs and active tasks keep a pending revision; resume/maintenance
// applies it without losing the saved mutation or exposing its plaintext.
func (s *Server) noteCubeConfigApply(w http.ResponseWriter, r *http.Request, appID string) string {
	sb, err := s.Store.CurrentSandboxForApp(r.Context(), appID)
	if err != nil || sb.RuntimeProvider != "cube" {
		return ""
	}
	state := "pending"
	if sb.Status == "running" && (s.Locks == nil || s.Locks.TryLock(sb.ID)) {
		func() {
			if s.Locks != nil {
				defer s.Locks.Unlock(sb.ID)
			}
			bounded, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			_ = s.syncCubeAppConfig(bounded, sb.ID)
		}()
	}
	state = s.cubeConfigState(r.Context(), appID)
	w.Header().Set("X-Sandboxd-Runtime-Config", state)
	return state
}

func (s *Server) validateCubeConfigRow(ctx context.Context, appID string, row *store.AppConfig) error {
	if row.AccessPolicy != "runtime_access" && row.AccessPolicy != "both" {
		return nil
	}
	sb, err := s.Store.CurrentSandboxForApp(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		bound, e := s.Store.AppUsesCube(ctx, appID)
		if e != nil {
			return e
		}
		if !bound && !s.CubeApps[appID] {
			return nil
		}
	} else if err != nil {
		return err
	} else if sb.RuntimeProvider != "cube" {
		return nil
	}
	value, err := s.effectiveConfigValue(row, nil)
	if err != nil {
		return errors.New("cannot decrypt runtime config")
	}
	return runtime.ValidateAppConfig(runtime.AppConfigRequest{Revision: "validation", Env: map[string]string{row.Key: value}})
}
