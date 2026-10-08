package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitProcessCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("process condition timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProcessResumeAfterRetryExhaustion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := newProcess("web", "web", dir, "test -f repaired || exit 1; exec sleep 120", filepath.Join(dir, "web.log"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.supervise(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		p.stop()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	waitProcessCondition(t, 25*time.Second, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.retryBlocked })
	if _, restarts, running := p.snapshot(); restarts != maxFastFails || running {
		t.Fatalf("failed process state: restarts=%d running=%v", restarts, running)
	}
	if err := os.WriteFile(filepath.Join(dir, "repaired"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, restarts, running := p.snapshot(); restarts != maxFastFails || running {
		t.Fatal("process retried without an explicit resume")
	}
	select {
	case <-done:
		t.Fatal("supervisor abandoned the process")
	default:
	}
	p.suspend()
	p.resume()
	waitProcessCondition(t, 2*time.Second, func() bool { _, _, running := p.snapshot(); return running })
	first, _, _ := p.snapshot()
	p.resume() // Repeated resume must not start a second child.
	time.Sleep(100 * time.Millisecond)
	if pid, _, running := p.snapshot(); pid != first || !running {
		t.Fatal("repeated resume replaced the running child")
	}
	p.suspend()
	if _, _, running := p.snapshot(); running {
		t.Fatal("suspend left child running")
	}
	p.resume()
	waitProcessCondition(t, 2*time.Second, func() bool { _, _, running := p.snapshot(); return running })
}

func TestProcessCancellationWhileRetryBlocked(t *testing.T) {
	p := newProcess("web", "web", t.TempDir(), "exit 1", "unused", slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.retryBlocked = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.supervise(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked supervisor ignored cancellation")
	}
}
