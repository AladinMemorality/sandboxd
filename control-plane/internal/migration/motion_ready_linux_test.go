package migration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestMotionReadinessRequiresWorkerResponsesThroughPrivateGuestIngress(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != "GET" || r.Host != "3000-target.cube.test" || r.Header.Get("cube-traffic-access-token") != "private-ingress" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected target identity or credential forwarding")
		}
		if r.URL.Path == "/api/status" {
			io.WriteString(w, `{"mode":"shared-workspace","hannibal":false}`)
		} else {
			io.WriteString(w, `{"projects":[]}`)
		}
	}))
	defer server.Close()
	if err := verifyMotionPreview(context.Background(), server.URL, "3000-target.cube.test", "private-ingress"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/api/status", "/api/projects"}) {
		t.Fatal("local health cannot substitute for actual worker requests", paths)
	}
}

func TestMotionReadinessRejectsMissingWorkerAndNeverFollowsRedirect(t *testing.T) {
	for _, scenario := range []string{"local-health", "worker-failed", "missing-projects", "redirect", "invalid-json"} {
		t.Run(scenario, func(t *testing.T) {
			redirected := false
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
			defer other.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "local-health":
					io.WriteString(w, `{"ok":true}`)
				case "worker-failed":
					w.WriteHeader(502)
				case "redirect":
					http.Redirect(w, r, other.URL, 302)
				case "invalid-json":
					io.WriteString(w, "not json")
				default:
					io.WriteString(w, `{"mode":"shared-workspace","projects":null}`)
				}
			}))
			defer server.Close()
			if verifyMotionPreview(context.Background(), server.URL, "3000-target.cube.test", "private-ingress") == nil {
				t.Fatal("unverified worker accepted")
			}
			if redirected {
				t.Fatal("private ingress credential followed a redirect")
			}
		})
	}
}
