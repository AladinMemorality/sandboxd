package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/metrics"
)

// CubeHandler is the application API for the replacement Cube controller.
// It shares durable project/task semantics, but has no Docker execution or
// host workspace provisioning routes. Configure it before serving any requests.
func (s *Server) CubeHandler(ctx context.Context) (http.Handler, error) {
	if s.Store == nil || s.Cube == nil || s.Secrets == nil || !s.CubeAllApps || len(s.CubeApps) != 0 || s.CubeReadiness == nil {
		return nil, errors.New("Cube controller requires global Cube, durable state, secrets and worker readiness")
	}
	if s.Docker != nil || s.Loopback != nil || s.Wake != nil || s.Upgrade != nil || s.Snapshot != nil || s.Image != "" {
		return nil, errors.New("Cube controller cannot contain Docker or host workspace lifecycle dependencies")
	}
	if err := s.Store.CheckCubeOnly(ctx); err != nil {
		return nil, err
	}
	s.cubeOnly = true
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.cubeControllerReady)
	mux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /sandboxes", s.observe("GET /sandboxes", s.handleList))
	// Keep the read-only platform admin response shape without Docker inspect.
	mux.HandleFunc("GET /sandbox/{id}", s.observe("GET /v1/sandboxes/{id}", s.cubeControllerGet))
	mux.HandleFunc("GET /preview-auth", s.handlePreviewAuth)
	mux.HandleFunc("GET /forward-auth", s.handleForwardAuth)
	mux.HandleFunc("POST /v1/cube-model/{sandboxID}/{taskID}/v1/messages", s.cubeModelRelay)
	mux.HandleFunc("POST /v1/cube-model/{sandboxID}/{taskID}/v1/messages/count_tokens", s.cubeModelRelay)
	mux.HandleFunc("GET /v1/sandboxes/{id}", s.observe("GET /v1/sandboxes/{id}", s.v1GetSandbox))
	mux.HandleFunc("POST /v1/sandboxes/{id}/stop", s.observe("POST /v1/sandboxes/{id}/stop", s.v1StopSandbox))
	mux.HandleFunc("POST /v1/sandboxes/{id}/start", s.observe("POST /v1/sandboxes/{id}/start", s.v1StartSandbox))
	mux.HandleFunc("POST /v1/sandboxes/{id}/preview-access", s.observe("POST /v1/sandboxes/{id}/preview-access", s.v1CubePreviewAccess))
	mux.HandleFunc("POST /v1/sandboxes/{id}/recreate", s.observe("POST /v1/sandboxes/{id}/recreate", s.v1RecreateSandbox))
	mux.HandleFunc("DELETE /v1/sandboxes/{id}", s.observe("DELETE /v1/sandboxes/{id}", s.v1DeleteSandbox))
	mux.HandleFunc("POST /v1/sandboxes/{id}/tasks", s.observe("POST /v1/sandboxes/{id}/tasks", s.v1SubmitTask))
	mux.HandleFunc("GET /v1/sandboxes/{id}/tasks", s.observe("GET /v1/sandboxes/{id}/tasks", s.v1ListTasks))
	mux.HandleFunc("GET /v1/sandboxes/{id}/tasks/{taskId}", s.observe("GET /v1/sandboxes/{id}/tasks/{taskId}", s.v1GetTask))
	mux.HandleFunc("POST /v1/sandboxes/{id}/tasks/{taskId}/revert", s.observe("POST /v1/sandboxes/{id}/tasks/{taskId}/revert", s.v1RevertTask))
	mux.HandleFunc("GET /v1/sandboxes/{id}/tasks/{taskId}/events", s.observe("GET /v1/sandboxes/{id}/tasks/{taskId}/events", s.v1TaskEvents))
	mux.HandleFunc("POST /v1/sandboxes/{id}/tasks/{taskId}/cancel", s.observe("POST /v1/sandboxes/{id}/tasks/{taskId}/cancel", s.v1CancelTask))
	mux.HandleFunc("POST /v1/sandboxes/{id}/tasks/{taskId}/messages", s.observe("POST /v1/sandboxes/{id}/tasks/{taskId}/messages", s.v1TaskMessage))
	mux.HandleFunc("GET /v1/sandboxes/{id}/files", s.observe("GET /v1/sandboxes/{id}/files", s.v1ListFiles))
	mux.HandleFunc("GET /v1/sandboxes/{id}/files/content", s.observe("GET /v1/sandboxes/{id}/files/content", s.v1FileContent))
	mux.HandleFunc("PUT /v1/sandboxes/{id}/files", s.observe("PUT /v1/sandboxes/{id}/files", s.v1PutFile))
	mux.HandleFunc("GET /v1/sandboxes/{id}/export", s.observe("GET /v1/sandboxes/{id}/export", s.v1Export))
	mux.HandleFunc("GET /v1/sandboxes/{id}/processes/{name}/logs", s.observe("GET /v1/sandboxes/{id}/processes/{name}/logs", s.v1ProcessLogs))
	mux.HandleFunc("GET /v1/settings", s.observe("GET /v1/settings", s.v1GetSettings))
	mux.HandleFunc("PATCH /v1/settings", s.observe("PATCH /v1/settings", s.v1PatchSettings))
	mux.HandleFunc("GET /v1/agents", s.observe("GET /v1/agents", s.v1ListAgents))
	mux.HandleFunc("POST /v1/agents/claude-code/oauth/start", s.observe("POST /v1/agents/claude-code/oauth/start", s.v1AgentOAuthStart))
	mux.HandleFunc("POST /v1/agents/claude-code/oauth/finish", s.observe("POST /v1/agents/claude-code/oauth/finish", s.v1AgentOAuthFinish))
	mux.HandleFunc("POST /v1/agents/{provider}/import", s.observe("POST /v1/agents/{provider}/import", s.v1AgentImport))
	mux.HandleFunc("POST /v1/agents/{provider}/api-key", s.observe("POST /v1/agents/{provider}/api-key", s.v1AgentAPIKey))
	mux.HandleFunc("POST /v1/agents/{provider}/disconnect", s.observe("POST /v1/agents/{provider}/disconnect", s.v1AgentDisconnect))
	mux.HandleFunc("GET /v1/presets", s.observe("GET /v1/presets", s.v1ListPresets))
	mux.HandleFunc("POST /v1/git-credentials", s.observe("POST /v1/git-credentials", s.v1CreateGitCredential))
	mux.HandleFunc("GET /v1/git-credentials", s.observe("GET /v1/git-credentials", s.v1ListGitCredentials))
	mux.HandleFunc("DELETE /v1/git-credentials/{id}", s.observe("DELETE /v1/git-credentials/{id}", s.v1DeleteGitCredential))
	mux.HandleFunc("GET /v1/auth/status", s.observe("GET /v1/auth/status", s.v1AuthStatus))
	mux.HandleFunc("POST /v1/auth/setup", s.observe("POST /v1/auth/setup", s.v1AuthSetup))
	mux.HandleFunc("POST /v1/auth/login", s.observe("POST /v1/auth/login", s.v1AuthLogin))
	mux.HandleFunc("POST /v1/auth/logout", s.observe("POST /v1/auth/logout", s.v1AuthLogout))
	mux.HandleFunc("POST /v1/auth/password", s.observe("POST /v1/auth/password", s.v1AuthPassword))
	mux.HandleFunc("GET /v1/api-keys", s.observe("GET /v1/api-keys", s.v1ListAPIKeys))
	mux.HandleFunc("POST /v1/api-keys", s.observe("POST /v1/api-keys", s.v1CreateAPIKey))
	mux.HandleFunc("DELETE /v1/api-keys/{id}", s.observe("DELETE /v1/api-keys/{id}", s.v1DeleteAPIKey))
	mux.HandleFunc("POST /v1/apps", s.observe("POST /v1/apps", s.v1CreateApp))
	mux.HandleFunc("GET /v1/apps", s.observe("GET /v1/apps", s.v1ListApps))
	mux.HandleFunc("GET /v1/apps/{id}", s.observe("GET /v1/apps/{id}", s.v1GetApp))
	mux.HandleFunc("PATCH /v1/apps/{id}", s.observe("PATCH /v1/apps/{id}", s.v1PatchApp))
	mux.HandleFunc("DELETE /v1/apps/{id}", s.observe("DELETE /v1/apps/{id}", s.v1DeleteApp))
	mux.HandleFunc("POST /v1/apps/{id}/sandbox", s.observe("POST /v1/apps/{id}/sandbox", s.v1CreateAppSandbox))
	mux.HandleFunc("GET /v1/apps/{id}/snapshots", s.observe("GET /v1/apps/{id}/snapshots", s.v1ListAppSnapshots))
	mux.HandleFunc("POST /v1/apps/{id}/restore", s.observe("POST /v1/apps/{id}/restore", s.v1RestoreApp))
	mux.HandleFunc("POST /v1/apps/{id}/fork", s.observe("POST /v1/apps/{id}/fork", s.v1ForkApp))
	mux.HandleFunc("GET /v1/apps/{id}/events", s.observe("GET /v1/apps/{id}/events", s.v1ListAppEvents))
	mux.HandleFunc("GET /v1/tasks/{id}/events", s.observe("GET /v1/tasks/{id}/events", s.v1ListTaskEvents))
	mux.HandleFunc("POST /v1/apps/{id}/config", s.observe("POST /v1/apps/{id}/config", s.v1CreateAppConfig))
	mux.HandleFunc("POST /v1/runtime/manifest/validate", s.observe("POST /v1/runtime/manifest/validate", s.v1ValidateManifest))
	mux.HandleFunc("GET /v1/runtime/recipes", s.observe("GET /v1/runtime/recipes", s.v1RuntimeRecipes))
	mux.HandleFunc("GET /v1/apps/{id}/config", s.observe("GET /v1/apps/{id}/config", s.v1ListAppConfig))
	mux.HandleFunc("POST /v1/apps/{id}/config/{key}/reveal", s.observe("POST /v1/apps/{id}/config/{key}/reveal", s.v1RevealAppConfig))
	mux.HandleFunc("PATCH /v1/apps/{id}/config/{key}", s.observe("PATCH /v1/apps/{id}/config/{key}", s.v1PatchAppConfig))
	mux.HandleFunc("DELETE /v1/apps/{id}/config/{key}", s.observe("DELETE /v1/apps/{id}/config/{key}", s.v1DeleteAppConfig))
	mux.HandleFunc("POST /v1/snapshots", s.observe("POST /v1/snapshots", s.v1CreateSnapshot))
	mux.HandleFunc("GET /v1/snapshots", s.observe("GET /v1/snapshots", s.v1ListSnapshots))
	mux.HandleFunc("GET /v1/snapshots/{id}", s.observe("GET /v1/snapshots/{id}", s.v1GetSnapshot))
	mux.HandleFunc("DELETE /v1/snapshots/{id}", s.observe("DELETE /v1/snapshots/{id}", s.v1DeleteSnapshot))

	// The exclusive daemon maintenance lock prevents an offline migration from
	// changing providers concurrently. Check again on every application request
	// so a corrupt/externally edited binding never enters a legacy handler.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			if err := s.Store.CheckCubeOnly(r.Context()); err != nil {
				writeV1Err(w, 503, "runtime_unavailable", "Cube fleet binding validation failed")
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func (s *Server) cubeControllerReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if s.Store.CheckCubeOnly(ctx) != nil || s.CubeReadiness(ctx) != nil {
		writeErr(w, 503, "Cube controller is not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

func (s *Server) cubeControllerGet(w http.ResponseWriter, r *http.Request) {
	sb, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return
	}
	// Read cached metadata only. An admin list must never wake all projects.
	writeJSON(w, 200, getResp{Row: toRespRow(sb), LiveState: nil})
}

// CubePreviewHandler routes stable preview authorities to authenticated Cube
// ingress directly. Unknown or legacy previews never fall through to an API or
// a Docker catch-all. Wrap apiHandler in service-token auth before passing it.
func (s *Server) CubePreviewHandler(apiHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		suffix := ".preview." + strings.ToLower(s.PreviewDomain)
		if strings.HasSuffix(host, suffix) || strings.Contains(host, suffix+":") {
			if !s.TryServeCubePreview(w, r) {
				http.NotFound(w, r)
			}
			return
		}
		apiHandler.ServeHTTP(w, r)
	})
}
