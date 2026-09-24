package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateWorkspaceFileClientScopesStreamsAndRejectsRedirects(t *testing.T) {
	payload := []byte("bounded archive fixture")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Host != "owner.cube.test" || r.Header.Get("Authorization") != "Bearer "+testRemoteToken || r.Header.Get("cube-traffic-access-token") != "private-ingress" {
			t.Error("missing scoped credentials")
		}
		switch r.URL.Path {
		case "/export/private-workspace-v2":
			w.Write(payload)
		case "/import/private-workspace-v2":
			b, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(b, payload) || r.ContentLength != int64(len(payload)) {
				t.Error("stream changed", err)
			}
		default:
			t.Error("wrong route")
		}
	}))
	defer srv.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: srv.URL, Token: testRemoteToken, Host: "owner.cube.test", TrafficAccessToken: "private-ingress"})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err = c.ExportPrivateWorkspaceFile(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	if err = c.ImportPrivateWorkspaceFile(context.Background(), bytes.NewReader(b.Bytes()), int64(b.Len())); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	for _, n := range []int64{-1, MaxPrivateWorkspaceStreamBytes + 1} {
		if err = c.ImportPrivateWorkspaceFile(context.Background(), strings.NewReader(""), n); err == nil {
			t.Fatal("bad size accepted")
		}
	}
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c, err = NewRemoteClient(RemoteConfig{BaseURL: redirect.URL, Token: testRemoteToken})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ExportPrivateWorkspaceFile(context.Background(), io.Discard); err == nil {
		t.Fatal("redirect accepted")
	}
	if hits != 0 {
		t.Fatal("credential followed redirect")
	}
	unavailable := errors.New("missing binding")
	c = NewUnavailableClient(unavailable)
	if err = c.ExportPrivateWorkspaceFile(context.Background(), io.Discard); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
	if err = c.ImportPrivateWorkspaceFile(context.Background(), strings.NewReader(""), 0); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
}
