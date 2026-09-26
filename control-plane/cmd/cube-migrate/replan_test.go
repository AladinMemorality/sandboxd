package main

import (
	"context"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReplanRequiresAuthoritativeRemoteNotFound(t *testing.T) {
	for _, status := range []int{200, 401, 404, 500, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write([]byte(`{"sandboxID":"former"}`))
		}))
		client, e := cube.New(cube.Config{APIURL: srv.URL, APIKey: "operator"})
		if e != nil {
			srv.Close()
			t.Fatal(e)
		}
		e = confirmReplanTargetGone(context.Background(), client, "former")
		srv.Close()
		if (e == nil) != (status == 404) {
			t.Fatalf("status%d: %v", status, e)
		}
	}
}
