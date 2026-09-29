package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGuestFileClientEscapesAndBounds(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("path")
		if got == "oversized" {
			io.CopyN(w, zeroReader{}, MaxFileReadBytes+1)
			return
		}
		if got == "error" {
			w.WriteHeader(403)
			io.WriteString(w, "secret guest response")
			return
		}
		io.WriteString(w, "okay")
	}))
	defer server.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: testRemoteToken})
	if err != nil {
		t.Fatal(err)
	}
	raw := "src/a?b&recursive=true#test"
	if _, err = c.ReadFile(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if got != raw {
		t.Fatalf("query injection: %q", got)
	}
	if _, err = c.ReadFile(context.Background(), "oversized"); err == nil {
		t.Fatal("oversized guest response accepted")
	}
	_, err = c.ReadFile(context.Background(), "error")
	var response *ResponseError
	if !errors.As(err, &response) || response.StatusCode != 403 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
	if _, err = c.PutFile(context.Background(), "large", io.LimitReader(zeroReader{}, MaxFileWriteBytes+1)); !errors.As(err, &response) || response.StatusCode != 413 {
		t.Fatalf("write cap: %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestTaskResultClientRejectsWrongIdentityAndNonterminalState(t *testing.T) {
	for _, body := range []string{`{"id":"other","status":"succeeded"}`, `{"id":"task1","status":"running"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			defer server.Close()
			c, _ := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: testRemoteToken})
			if _, err := c.TaskResult(context.Background(), "task1"); err == nil {
				t.Fatal("invalid task result accepted")
			}
		})
	}
}

func TestTransferUsesOverallBudgetInsteadOfRPCHeaderDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(80 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		if r.Method == http.MethodPut {
			io.WriteString(w, `{"path":"image.png","size":4}`)
		} else {
			io.WriteString(w, "export")
		}
	}))
	defer server.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: testRemoteToken})
	if err != nil {
		t.Fatal(err)
	}
	transport := c.stream.Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	c.stream.Transport = transport
	transport.ResponseHeaderTimeout = 10 * time.Millisecond
	if _, err := c.PutFile(context.Background(), "image.png", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExportWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	if transport.ResponseHeaderTimeout != 10*time.Millisecond {
		t.Fatal("shared RPC deadline changed")
	}
	if _, err := c.ReadFile(context.Background(), "file"); err == nil {
		t.Fatal("ordinary RPC unexpectedly ignored its deadline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if _, err := c.PutFile(ctx, "image.png", strings.NewReader("data")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
}

func TestFileReadAllowsSlowBodyButKeepsCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-time.After(80 * time.Millisecond):
			io.WriteString(w, "build asset")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: testRemoteToken})
	if err != nil {
		t.Fatal(err)
	}
	c.http.Timeout = 10 * time.Millisecond
	if data, err := c.ReadFile(context.Background(), "dist/asset.js"); err != nil || string(data) != "build asset" {
		t.Fatalf("bounded file transfer inherited RPC body deadline: %v", err)
	}
	if _, err := c.ListFiles(context.Background(), ".", false); !os.IsTimeout(err) {
		t.Fatalf("ordinary RPC deadline lost: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.ReadFile(ctx, "dist/asset.js"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("file caller deadline lost: %v", err)
	}
}
