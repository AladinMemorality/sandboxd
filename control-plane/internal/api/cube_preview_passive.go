package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Only Vite's exact protocol is passive. Other application WebSockets and SSE
// still hold an activity lease. Ambiguous multi-protocol offers stay active.
func passiveCubePreview(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if len(r.Header.Values("Accept")) == 1 && r.Header.Get("Accept") == "text/x-vite-ping" {
		return true
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || len(r.Header.Values("Upgrade")) != 1 ||
		len(r.Header.Values("Sec-WebSocket-Protocol")) != 1 || r.Header.Get("Sec-WebSocket-Protocol") != "vite-hmr" {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func validCubeAdmissionIdentity(sb *store.Sandbox, b *store.RuntimeBinding, a cube.AdmissionRecord, remote *cube.Sandbox) bool {
	return sb != nil && b != nil && remote != nil && sb.Status == "running" && sb.RuntimeProvider == "cube" && sb.AppID.Valid &&
		b.SandboxID == sb.ID && b.Provider == "cube" && a.RuntimeID == b.RuntimeID && a.TemplateID == b.TemplateID &&
		a.Key == "app:"+sb.AppID.String && a.State == "active" && a.Charged == 1 && a.Token != "" &&
		remote.SandboxID == b.RuntimeID && remote.TemplateID == b.TemplateID &&
		remote.Metadata["sandboxd_id"] == sb.ID && remote.Metadata["sandboxd_app_id"] == sb.AppID.String
}

func validPassiveCubeBinding(sb *store.Sandbox, b *store.RuntimeBinding, a cube.AdmissionRecord, remote *cube.Sandbox) bool {
	return validCubeAdmissionIdentity(sb, b, a, remote) && remote.State == "running"
}

// No Connect, config synchronization, activity write or lease renewal occurs.
// Admission Create and CreateRecovery require autoResume=false. The pinned CLM
// resumer rejects a paused->running transition with that immutable policy even
// if a pause races this GET before CubeProxy receives the request. Unknown or
// pre-admission bindings cannot establish that contract and are refused.
func (s *Server) checkPassiveCubePreview(ctx context.Context, id string, binding *store.RuntimeBinding) error {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	sb, err := s.Store.Get(bounded, id)
	if err != nil || sb.Status != "running" {
		return errors.New("passive preview requires running app")
	}
	current, err := s.Store.GetRuntimeBinding(bounded, id)
	if err != nil || current.RuntimeID != binding.RuntimeID || current.TemplateID != binding.TemplateID || current.Domain != binding.Domain {
		return errors.New("passive preview binding changed")
	}
	admission, err := s.Store.AdmissionLookup(bounded, binding.RuntimeID)
	if err != nil || admission.State != "active" || admission.Charged != 1 {
		return errors.New("passive preview admission unavailable")
	}
	remote, err := s.Cube.Get(bounded, binding.RuntimeID)
	if err != nil || !validPassiveCubeBinding(sb, current, admission, remote) {
		return errors.New("passive preview running state unavailable")
	}
	latest, err := s.Store.AdmissionLookup(bounded, binding.RuntimeID)
	if err != nil || latest != admission {
		return errors.New("passive preview admission changed")
	}
	return nil
}

func writePassiveCubeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Retry-After", "5")
	writeV1Err(w, 503, "preview_asleep", "The app is not active. Open its app page to start it.")
}
