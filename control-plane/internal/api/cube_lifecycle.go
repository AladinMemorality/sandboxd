package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// guardCubeRoute is an allowlist: unported routes must not operate on host
// files or Docker objects merely because Cube shares the sandbox ID format.
func (s *Server) guardCubeRoute(w http.ResponseWriter, r *http.Request, endpoint string) bool {
	if s.Store == nil {
		return false
	}
	id := r.PathValue("id")
	if id == "" {
		return false
	}
	var sb *store.Sandbox
	var err error
	if strings.Contains(endpoint, "/sandboxes/{id}") || strings.Contains(endpoint, "/sandbox/{id}") || strings.Contains(endpoint, "/wake/{id}") {
		sb, err = s.Store.Get(r.Context(), id)
	} else if strings.Contains(endpoint, "/apps/{id}") {
		sb, err = s.Store.CurrentSandboxForApp(r.Context(), id)
	} else {
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot resolve sandbox runtime")
		return true
	}
	if sb.RuntimeProvider != "cube" {
		return false
	}
	if !sb.AppID.Valid {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	if _, err := s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	switch endpoint {
	case "GET /v1/sandboxes/{id}", "POST /v1/sandboxes/{id}/start", "POST /v1/sandboxes/{id}/stop", "DELETE /v1/sandboxes/{id}", "GET /v1/apps/{id}":
		return false
	default:
		writeV1Err(w, http.StatusNotImplemented, "cube_operation_unsupported", "this operation is not yet implemented for Cube sandboxes")
		return true
	}
}

func (s *Server) createCubeAppSandbox(w http.ResponseWriter, r *http.Request, app *store.App, req v1CreateAppSandboxReq, preset string) {
	if s.Cube == nil || s.Secrets == nil || s.CubeProxyURL == "" || s.CubeDomain == "" {
		writeV1Err(w, 503, "runtime_unavailable", "Cube runtime is not configured")
		return
	}
	template := s.CubeTemplates[preset]
	if preset == "" || template == "" {
		writeV1Err(w, 400, "invalid_request", "runtime preset is not enabled for Cube")
		return
	}
	if req.Template != "" || app.GitRepoURL.Valid {
		writeV1Err(w, 501, "cube_operation_unsupported", "Cube Git import and custom templates are not implemented")
		return
	}
	for _, port := range req.Ports {
		if port != presetWebPort(preset) {
			writeV1Err(w, 400, "invalid_request", "Cube ports are fixed by the trusted template")
			return
		}
	}
	id := newULID()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeV1Err(w, 500, "internal", "cannot generate supervisor credential")
		return
	}
	token := hex.EncodeToString(tokenBytes)
	// Initial pilot deliberately denies all outbound traffic. Public previews,
	// package registries and model proxy access need an operator-reviewed policy.
	remote, err := s.Cube.Create(r.Context(), cube.CreateRequest{TemplateID: template, TimeoutSeconds: 3600,
		EnvVars:   map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": token},
		Metadata:  map[string]string{"sandboxd_id": id, "sandboxd_app_id": app.ID},
		Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false},
		Network:   &cube.NetworkPolicy{AllowPublicTraffic: false, DenyOut: []string{"0.0.0.0/0", "::/0"}},
	})
	if err != nil {
		writeV1Err(w, 502, "runtime_unavailable", "Cube creation failed")
		return
	}
	if remote.TrafficAccessToken == "" {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.Cube.Delete(cleanup, remote.SandboxID)
		writeV1Err(w, 502, "runtime_unavailable", "Cube returned no private ingress credential")
		return
	}
	credentials, _ := json.Marshal(cubeCredentials{SupervisorToken: token, TrafficAccessToken: remote.TrafficAccessToken})
	ciphertext, nonce, err := s.Secrets.Seal(credentials)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.Cube.Delete(cleanup, remote.SandboxID)
		writeV1Err(w, 500, "internal", "cannot encrypt runtime credentials")
		return
	}
	binding := &store.RuntimeBinding{SandboxID: id, Provider: "cube", RuntimeID: remote.SandboxID, TemplateID: template, Domain: s.CubeDomain, TokenCiphertext: ciphertext, TokenNonce: nonce}
	sb := &store.Sandbox{ID: id, Status: "creating", Image: "cube-template:" + template, RuntimeProvider: "cube", RuntimeBinding: binding,
		AppID: sql.NullString{String: app.ID, Valid: true}, ExternalUserID: app.ExternalUserID, ExternalProjectID: app.ExternalProjectID,
		Visibility: "private", IdlePolicy: "sleep", Ports: []int{presetWebPort(preset)}, WebPort: sql.NullInt64{Int64: int64(presetWebPort(preset)), Valid: true}}
	if err = s.Store.Create(r.Context(), sb); err != nil {
		// A canceled request must still clean up the remote VM. No local row points
		// to it unless Create atomically committed the sandbox and runtime binding.
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Check after a canceled writer wait; it may have committed successfully.
		if _, e := s.Store.Get(cleanup, id); e == nil {
			writeV1Err(w, 503, "runtime_unavailable", "Cube runtime binding retained; supervisor readiness is unverified")
			return
		}
		if e := s.Cube.Delete(cleanup, remote.SandboxID); e != nil && s.Log != nil {
			s.Log.Error("orphan Cube sandbox requires cleanup", "runtime_id", remote.SandboxID)
		}
		writeV1Err(w, 500, "internal", "cannot persist Cube runtime binding")
		return
	}
	// Cube API creation alone cannot prove that this fresh per-instance token
	// was applied to the restored supervisor. Authenticate before advertising it.
	readyCtx, cancelReady := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancelReady()
	for {
		if _, err = s.runtimeClientFor(id).Status(readyCtx); err == nil {
			break
		}
		select {
		case <-readyCtx.Done():
			persist, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.Store.MarkError(persist, id, "Cube supervisor authentication/readiness failed")
			writeV1Err(w, 502, "runtime_unavailable", "Cube supervisor not ready; retained runtime binding for recovery")
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err = s.Store.MarkRunningWoke(r.Context(), id, "", "", time.Now().UTC()); err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot persist Cube readiness")
		return
	}
	sb, err = s.Store.Get(r.Context(), id)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot read created sandbox")
		return
	}
	writeJSON(w, http.StatusCreated, toRespRow(sb))
}

type cubeCredentials struct {
	SupervisorToken    string `json:"supervisor_token"`
	TrafficAccessToken string `json:"traffic_access_token"`
}

func (s *Server) cubeRuntimeClient(id string) (*runtime.Client, bool) {
	if s.Store == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	isCube, err := s.Store.IsCube(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false
	}
	if err != nil {
		return runtime.NewUnavailableClient(err), true
	}
	if !isCube {
		return nil, false
	}
	b, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		return runtime.NewUnavailableClient(err), true
	}
	if s.Secrets == nil || s.CubeProxyURL == "" {
		return runtime.NewUnavailableClient(errors.New("Cube supervisor is unavailable")), true
	}
	token, err := s.Secrets.Open(b.TokenCiphertext, b.TokenNonce)
	if err != nil {
		return runtime.NewUnavailableClient(errors.New("cannot decrypt Cube supervisor credential")), true
	}
	var credentials cubeCredentials
	if json.Unmarshal(token, &credentials) != nil || credentials.SupervisorToken == "" || credentials.TrafficAccessToken == "" {
		return runtime.NewUnavailableClient(errors.New("invalid Cube runtime credentials")), true
	}
	client, err := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: s.CubeProxyURL, Token: credentials.SupervisorToken, TrafficAccessToken: credentials.TrafficAccessToken, Host: fmt.Sprintf("3031-%s.%s", b.RuntimeID, b.Domain)})
	if err != nil {
		return runtime.NewUnavailableClient(err), true
	}
	return client, true
}

func (s *Server) cubeLifecycle(w http.ResponseWriter, r *http.Request, action string) bool {
	id := r.PathValue("id")
	sb, err := s.Store.Get(r.Context(), id)
	if err != nil || sb.RuntimeProvider != "cube" {
		return false
	}
	if !sb.AppID.Valid {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	if _, err = s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	if s.Cube == nil {
		writeV1Err(w, 503, "runtime_unavailable", "Cube runtime is disabled")
		return true
	}
	b, err := s.Store.GetRuntimeBinding(r.Context(), id)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "Cube runtime binding unavailable")
		return true
	}
	if s.Locks != nil {
		s.Locks.Lock(id)
		defer s.Locks.Unlock(id)
	}
	sb, err = s.Store.Get(r.Context(), id)
	if err != nil {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	switch action {
	case "pause":
		// Fail closed when task state cannot be read: pausing an unknown active
		// write/task could interrupt application state or coding work.
		if sb.Status != "stopped" {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			status, e := s.runtimeClientFor(id).Status(ctx)
			if e != nil {
				writeV1Err(w, 503, "runtime_unavailable", "cannot verify active tasks before pause")
				return true
			}
			if status.ActiveTask != nil {
				writeV1Err(w, 409, "task_in_progress", "cancel the active task before stopping")
				return true
			}
			err = s.Cube.Pause(r.Context(), b.RuntimeID)
			if err == nil {
				err = s.Store.MarkStoppedAt(r.Context(), id, time.Now().UTC())
			}
		}
	case "connect":
		_, err = s.Cube.Connect(r.Context(), b.RuntimeID, cube.ConnectRequest{})
		if err == nil {
			err = s.Store.MarkRunningWoke(r.Context(), id, "", "", time.Now().UTC())
		}
	case "delete":
		err = s.Cube.Delete(r.Context(), b.RuntimeID)
		var apiErr *cube.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			err = nil
		}
		if err == nil {
			// Finish local deletion even if the caller disconnects after the
			// destructive remote operation. A retry can recover a remote404.
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = s.Store.PurgeSandbox(cleanup, id)
		}
	default:
		err = errors.New("unsupported Cube lifecycle action")
	}
	if err != nil {
		writeV1Err(w, 502, "runtime_unavailable", "Cube lifecycle operation failed")
		return true
	}
	if action == "delete" {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	sb, err = s.Store.Get(r.Context(), id)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot read sandbox state")
		return true
	}
	writeJSON(w, 200, s.v1SandboxFromRow(r, sb))
	return true
}
