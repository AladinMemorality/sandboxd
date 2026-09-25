package api

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCubePreviewCapacityNavigationAndAPIs(t *testing.T) {
	for _, tc := range []struct {
		method, mode, accept string
		html                 bool
	}{{"GET", "navigate", "text/html", true}, {"GET", "", "text/html", true}, {"GET", "cors", "text/html", false}, {"GET", "", "application/json", false}, {"POST", "navigate", "text/html", false}} {
		r := httptest.NewRequest(tc.method, "https://preview.test/?token=secret%3Cscript%3E", nil)
		r.Header.Set("Sec-Fetch-Mode", tc.mode)
		r.Header.Set("Accept", tc.accept)
		w := httptest.NewRecorder()
		if !writeCubePreviewAdmission(w, r, cube.ErrCapacityUnavailable) || w.Code != 503 || w.Header().Get("Retry-After") != "5" {
			t.Fatal("capacity contract lost")
		}
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") != tc.html {
			t.Fatal("wrong response dialect")
		}
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "<script") {
			t.Fatal("request data reflected")
		}
		if tc.html && (!strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(w.Body.String(), `http-equiv="refresh"`)) {
			t.Fatal("unsafe or nonretrying document")
		}
	}
}
func TestCubePreviewRecoveryIsNotAutomaticallyRetried(t *testing.T) {
	r := httptest.NewRequest("GET", "https://preview.test/", nil)
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w := httptest.NewRecorder()
	writeCubePreviewAdmission(w, r, cube.ErrRuntimeUnavailable)
	if w.Header().Get("Retry-After") != "" || strings.Contains(w.Body.String(), "http-equiv") {
		t.Fatal("recovery was retried")
	}
}
