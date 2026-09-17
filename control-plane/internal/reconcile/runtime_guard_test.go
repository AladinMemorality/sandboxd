package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestReconcileDoesNotInspectOrExpireRemoteRuntime(t *testing.T) {
	for _, status := range []string{"running", "stopped", "creating"} {
		t.Run(status, func(t *testing.T) {
			d := &Deps{}
			result := Result{}
			d.reconcileRow(context.Background(), &store.Sandbox{ID: "cube", RuntimeProvider: "cube", Status: status, CreatedAt: time.Now().Add(-time.Hour)}, &result)
			if result.Stopped != 0 || result.Errored != 0 {
				t.Fatalf("changed remote state: %+v", result)
			}
		})
	}
}
