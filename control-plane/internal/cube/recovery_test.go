package cube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestGetUnavailableRuntimeReturnsTypedError(t *testing.T) {
	for _, state := range []string{"stopped", "unknown"} {
		t.Run(state, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/sandboxes/vm-owned" {
					t.Error("unexpected mutation or identity")
				}
				// A crashed task may also have incomplete template metadata.
				json.NewEncoder(w).Encode(Sandbox{SandboxID: "vm-owned", State: state})
			})
			out, err := c.Get(context.Background(), "vm-owned")
			if out != nil || !errors.Is(err, ErrRuntimeUnavailable) {
				t.Fatalf("unavailable task accepted: out=%v error=%v", out, err)
			}
		})
	}
}

func TestUnavailableRuntimeDoesNotBypassIdentityValidation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(Sandbox{SandboxID: "vm-other", State: "unknown"})
	})
	_, err := c.Get(context.Background(), "vm-owned")
	if err == nil || errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("wrong provider identity accepted: %v", err)
	}
}
