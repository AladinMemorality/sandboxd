// capture-init prepares a credential-free Chromium VM before Cube snapshots it.
// It offers only bootstrap health/init and one authenticated capture channel.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"golang.org/x/sys/unix"
)

func workerEnvironment(socket string) []string {
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "USER=sandbox", "LOGNAME=sandbox", "LANG=C.UTF-8", "NODE_ENV=production", "PLAYWRIGHT_BROWSERS_PATH=/opt/browsers", "CAPTURE_WORKER_SOCKET=" + socket}
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("baarcha-capture-init 0.1.0")
		return
	}
	if err := runtime.RestrictPrivilegeGain(); err != nil {
		log.Fatal("cannot restrict capture privileges")
	}
	if os.Geteuid() == 0 {
		if syscall.Setgroups([]int{1000}) != nil || syscall.Setgid(1000) != nil || syscall.Setuid(1000) != nil {
			log.Fatal("cannot set capture identity")
		}
	}
	if os.Geteuid() != 1000 || os.Getegid() != 1000 {
		log.Fatal("capture requires uid/gid1000")
	}
	groups, err := os.Getgroups()
	if err != nil {
		log.Fatal("cannot verify capture groups")
	}
	for _, g := range groups {
		if g != 1000 {
			log.Fatal("unexpected capture group")
		}
	}
	if unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0}) != nil {
		log.Fatal("cannot disable capture core dumps")
	}
	if runtime.ProtectSupervisorProcess() != nil {
		log.Fatal("cannot protect capture supervisor")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	directory, err := os.MkdirTemp("/tmp", "capture-control-")
	if err != nil {
		log.Fatal("cannot create capture control directory")
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "worker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		log.Fatal("cannot listen for capture worker")
	}
	defer listener.Close()
	if os.Chmod(socket, 0600) != nil {
		log.Fatal("cannot protect capture worker socket")
	}
	b := &captureBootstrap{}
	server := func(addr string, h http.Handler) *http.Server {
		return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	}
	bootstrap := server(":49983", http.HandlerFunc(b.bootstrap))
	bootstrap.SetKeepAlivesEnabled(false)
	control := server(":3031", http.HandlerFunc(b.control))
	exited := make(chan error, 4)
	for _, srv := range []*http.Server{bootstrap, control} {
		go func(s *http.Server) { exited <- s.ListenAndServe() }(srv)
	}
	cmd := exec.CommandContext(ctx, "/usr/local/bin/node", "--max-old-space-size=192", "/worker/worker.mjs")
	cmd.Env = workerEnvironment(socket)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Worker output never includes a bootstrap request or control credential.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Start() != nil {
		log.Fatal("cannot start capture worker")
	}
	workerExited := make(chan error, 1)
	go func() { workerExited <- cmd.Wait() }()
	go func() {
		deadline := time.Now().Add(40 * time.Second)
		if unix, ok := listener.(*net.UnixListener); ok {
			_ = unix.SetDeadline(deadline)
		}
		peer, e := listener.Accept()
		if e != nil {
			exited <- e
			return
		}
		_ = listener.Close()
		_ = peer.SetReadDeadline(deadline)
		// Read one small readiness line without buffering any subsequent job data.
		ready := make([]byte, len("{\"type\":\"ready\"}\n"))
		_, e = io.ReadFull(peer, ready)
		if e != nil || string(ready) != "{\"type\":\"ready\"}\n" {
			_ = peer.Close()
			exited <- errors.New("capture worker not ready")
			return
		}
		_ = peer.SetReadDeadline(time.Time{})
		b.mu.Lock()
		b.worker = peer
		b.ready = true
		b.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
	case <-exited:
		cancel()
	case <-workerExited:
		b.mu.Lock()
		b.ready = false
		consumed := b.used
		b.mu.Unlock()
		if consumed {
			// Retain a consumed supervisor until host deletion. Exiting PID1 here can
			// kill the guest network before the final screenshot bytes drain. Never
			// restart or reuse the renderer, and let its bridge drain the UDS to EOF.
			select {
			case <-ctx.Done():
			case <-exited:
				cancel()
			}
		} else {
			cancel()
		}
	}
	_ = bootstrap.Close()
	_ = control.Close()
	_ = listener.Close()
	b.mu.Lock()
	if b.worker != nil {
		_ = b.worker.Close()
	}
	b.mu.Unlock()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
