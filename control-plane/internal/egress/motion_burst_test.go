package egress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMotionStudioBrowserBurstQueuesWithoutExceedingWorkerLimit(t *testing.T) {
	var active, maximum atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return okMotionResponse("poster"), nil
	}))
	done := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, motionRequest("GET", "/media/"+motionUUID+"/poster.png"))
			done <- w.Code
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("worker request did not enter")
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(h.waiters) != 4 {
		if time.Now().After(deadline) {
			t.Fatal("browser burst did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	for i := 0; i < 8; i++ {
		select {
		case code := <-done:
			if code != 200 {
				t.Fatalf("browser request returned %d", code)
			}
		case <-time.After(time.Second):
			t.Fatal("queued request leaked")
		}
	}
	if maximum.Load() != 4 || len(h.slots) != 0 || len(h.waiters) != 0 {
		t.Fatal("worker bound or cleanup failed")
	}
}

func TestMotionStudioQueuedCancellationAndRevocation(t *testing.T) {
	h := motionHandler(t, serviceRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("revoked request reached worker")
		return nil, nil
	}))
	for i := 0; i < 4; i++ {
		h.slots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if h.acquire(ctx) || len(h.waiters) != 0 {
		t.Fatal("cancelled waiter retained")
	}
	var authorized atomic.Bool
	authorized.Store(true)
	h.authorize = func(context.Context, Identity, string) bool { return authorized.Load() }
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, motionRequest("GET", "/api/status"))
		done <- w.Code
	}()
	deadline := time.Now().Add(time.Second)
	for len(h.waiters) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("request did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	authorized.Store(false)
	<-h.slots
	select {
	case code := <-done:
		if code != 403 {
			t.Fatalf("revoked request returned %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked waiter leaked")
	}
	if len(h.slots) != 3 || len(h.waiters) != 0 {
		t.Fatal("revoked request retained admission")
	}
}
