package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

func TestCubeRecoveryErrorIsGenericAndDoesNotSuggestRecreation(t *testing.T) {
	w := httptest.NewRecorder()
	if !writeCubeAdmissionError(w, fmt.Errorf("private-token /data/private: %w", cube.ErrRuntimeUnavailable)) {
		t.Fatal("unavailable runtime error was not handled")
	}
	body := w.Body.String()
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"runtime_recovery_required"`) {
		t.Fatalf("unexpected recovery response: %d %s", w.Code, body)
	}
	for _, secret := range []string{"private-token", "/data/private"} {
		if strings.Contains(body, secret) {
			t.Fatal("private upstream details leaked")
		}
	}
	if w.Header().Get("Retry-After") != "" {
		t.Fatal("recovery incorrectly advertised as a retryable capacity rejection")
	}
}
