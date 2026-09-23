// cube-init is a single-use bootstrap for a clean Cube template. The template
// waits without a credential; Cubelet supplies per-instance envVars at /init.
// The worker network must restrict this port to the trusted control plane.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

type bootstrap struct {
	mu      sync.Mutex
	started bool
	digest  [32]byte
	start   func(map[string]string) error
}

type initRequest struct {
	EnvVars map[string]string `json:"envVars"`
}

func (b *bootstrap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/health" {
		w.WriteHeader(200)
		return
	}
	if r.Method != "POST" || r.URL.Path != "/init" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(body) > 8192 {
		http.Error(w, "invalid init request", 400)
		return
	}
	var req initRequest
	if json.Unmarshal(body, &req) != nil || len(req.EnvVars) != 2 || req.EnvVars["RUNTIMED_HTTP_ADDR"] != ":3031" || runtime.ValidateRemoteToken(req.EnvVars["RUNTIMED_HTTP_TOKEN"]) != nil {
		http.Error(w, "invalid init configuration", 400)
		return
	}
	// Hash the normalized request to make retries independent of JSON key order.
	normalized, _ := json.Marshal(req)
	digest := sha256.Sum256(normalized)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		if digest != b.digest {
			http.Error(w, "already initialized", 409)
			return
		}
		w.WriteHeader(204)
		return
	}
	if err = b.start(req.EnvVars); err != nil {
		http.Error(w, "supervisor start failed", 503)
		return
	}
	b.digest = digest
	b.started = true
	w.WriteHeader(204)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("baarcha-cube-init 0.1.0")
		return
	}
	if err := runtime.RestrictPrivilegeGain(); err != nil {
		log.Fatal("cannot restrict guest privilege gain")
	}
	// Cube v0.7.1 does not preserve OCI USER when preparing this template.
	// Enforce the guest identity before accepting init or launching app code.
	if os.Geteuid() == 0 {
		if err := syscall.Setgroups([]int{1000}); err != nil {
			log.Fatal("cannot drop guest supplementary groups")
		}
		if err := syscall.Setgid(1000); err != nil {
			log.Fatal("cannot drop guest group")
		}
		if err := syscall.Setuid(1000); err != nil {
			log.Fatal("cannot drop guest user")
		}
	}
	if os.Geteuid() != 1000 || os.Getegid() != 1000 {
		log.Fatal("guest bootstrap requires sandbox uid/gid 1000")
	}
	groups, err := os.Getgroups()
	if err != nil {
		log.Fatal("cannot verify guest supplementary groups")
	}
	for _, group := range groups {
		if group != 1000 {
			log.Fatal("guest has unexpected supplementary group")
		}
	}
	if err := runtime.ProtectSupervisorProcess(); err != nil {
		log.Fatal("cannot protect guest bootstrap process")
	}
	_ = os.Setenv("HOME", "/home/sandbox")
	_ = os.Setenv("USER", "sandbox")
	_ = os.Setenv("LOGNAME", "sandbox")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	exited := make(chan error, 1)
	b := &bootstrap{start: func(vars map[string]string) error {
		cmd := exec.CommandContext(ctx, "/usr/local/bin/runtimed")
		cmd.Env = append(os.Environ(), "RUNTIMED_CUBE_GUEST=1", "RUNTIMED_HTTP_ADDR="+vars["RUNTIMED_HTTP_ADDR"], "RUNTIMED_HTTP_TOKEN="+vars["RUNTIMED_HTTP_TOKEN"])
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { exited <- cmd.Wait() }()
		return nil
	}}
	srv := &http.Server{Addr: ":49983", Handler: b, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		err := srv.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			exited <- err
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-exited:
		if err != nil {
			log.Print("guest supervisor/bootstrap stopped")
		}
		stop()
	}
	_ = srv.Close()
}
