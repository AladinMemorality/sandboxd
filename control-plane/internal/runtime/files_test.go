package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
