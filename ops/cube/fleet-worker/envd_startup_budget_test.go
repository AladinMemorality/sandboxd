package cubebox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestFleetEnvdInitializationToleratesRestoreSchedulingDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil { t.Fatal(err) }
	port, err := strconv.Atoi(u.Port())
	if err != nil { t.Fatal(err) }
	l := &local{envdHTTPClient: server.Client()}
	if err := l.doCreateTimeEnvdInitWithRetry(context.Background(), "127.0.0.1", port, true, []byte(`{"envVars":{"TEST":"value"}}`)); err != nil {
		t.Fatalf("a ready guest with 300ms scheduling delay must initialize: %v", err)
	}
}

func TestFleetEnvdInitializationStillHonorsCallerDeadline(t *testing.T) {
	l := &local{envdHTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := l.doCreateTimeEnvdInitWithRetry(ctx, "127.0.0.1", 49983, true, []byte(`{}`)); err == nil {
		t.Fatal("unresponsive initialization must fail")
	}
	if time.Since(started) > time.Second { t.Fatal("initialization ignored caller deadline") }
}
