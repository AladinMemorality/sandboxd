package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

// remoteControl is opt-in; the Unix listener remains available in both modes.
type remoteControl struct {
	Address string
	Token   string
}

func (c remoteControl) validate() error {
	if c.Address == "" {
		return nil
	}
	return runtime.ValidateRemoteToken(c.Token)
}

// serve preserves the default Unix-only control transport.
func serve(ctx context.Context, socketPath string, a *app) error {
	return serveControl(ctx, socketPath, a, remoteControl{})
}

// serveControl binds both listeners before accepting requests and closes both
// when either fails or ctx ends. Only the optional TCP listener requires auth.
func serveControl(ctx context.Context, socketPath string, a *app, remote remoteControl) error {
	if err := remote.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	unix, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer unix.Close()
	defer os.Remove(socketPath)

	handler := a.controlHandler()
	newServer := func(h http.Handler) *http.Server {
		return &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8192,
			BaseContext: func(net.Listener) context.Context { return ctx }}
	}
	local := newServer(handler)
	defer local.Close()
	errors := make(chan error, 2)
	var tcp net.Listener
	var network *http.Server
	if remote.Address != "" {
		tcp, err = net.Listen("tcp", remote.Address)
		if err != nil {
			return err
		}
		defer tcp.Close()
		network = newServer(authenticatedControl(remote.Token, handler))
		defer network.Close()
	}
	go func() { errors <- local.Serve(unix) }()
	a.log.Info("runtimed control socket listening", "socket", socketPath)
	if network != nil {
		go func() { errors <- network.Serve(tcp) }()
		a.log.Info("runtimed authenticated control HTTP listening", "address", tcp.Addr().String())
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errors:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// authenticatedControl authenticates before routing, including unknown paths.
// Hashing both inputs gives constant-length input to the constant-time compare.
func authenticatedControl(token string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		supplied := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(supplied[:], expected[:]) != 1 || len(r.Header.Values("Authorization")) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /tasks", a.handleStartTask)
	mux.HandleFunc("POST /config", a.handleAppConfig)
	mux.HandleFunc("GET /tasks", a.handleListTasks)
	mux.HandleFunc("GET /tasks/{id}/result", a.handleTaskResult)
	mux.HandleFunc("GET /files", a.handleFileList)
	mux.HandleFunc("GET /files/content", a.handleFileRead)
	mux.HandleFunc("PUT /files", a.handleFileWrite)
	mux.HandleFunc("GET /export", a.handleFileExport)
	mux.HandleFunc("POST /export/private-home", a.handlePrivateHome)
	mux.HandleFunc("PUT /import/private-home", a.handlePrivateHome)
	mux.HandleFunc("POST /export/private-task-history", a.handlePrivateTaskHistory)
	mux.HandleFunc("PUT /import/private-task-history", a.handlePrivateTaskHistory)
	mux.HandleFunc("POST /workspace/quiesce", a.handleWorkspaceQuiesce)
	mux.HandleFunc("POST /workspace/resume", a.handleWorkspaceResume)
	mux.HandleFunc("GET /export/private-workspace", a.handlePrivateWorkspaceExport)
	mux.HandleFunc("PUT /import/private-workspace", a.handlePrivateWorkspaceImport)
	mux.HandleFunc("GET /export/private-workspace-v2", a.handlePrivateWorkspaceFile)
	mux.HandleFunc("PUT /import/private-workspace-v2", a.handlePrivateWorkspaceFile)
	mux.HandleFunc("PUT /import/git-workspace", a.handlePrivateWorkspaceImport)
	mux.HandleFunc("GET /export/source", a.handleSourceExport)
	mux.HandleFunc("PUT /import/source", a.handleSourceImport)
	mux.HandleFunc("GET /processes/{name}/logs", a.handleProcessLogs)
	mux.HandleFunc("GET /tasks/{id}/events", a.handleTaskEvents)
	mux.HandleFunc("POST /tasks/{id}/cancel", a.handleCancelTask)
	mux.HandleFunc("POST /tasks/{id}/messages", a.handleTaskMessage)
	mux.HandleFunc("POST /tasks/{id}/revert", a.handleRevertTask)

	return a.workspaceFence(mux)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
