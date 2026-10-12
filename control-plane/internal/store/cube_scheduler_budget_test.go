package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// The observer reports space below CubeMaster's scheduler ceiling. At the
// ceiling a create must leave no pending intent, and the SAME project must be
// retryable once a newer observation confirms room, without a provider replay.
func TestSchedulerDiskBudgetRejectsBeforeReservationAndCanRetry(t *testing.T) {
	s := openTestStore(t)
	var posts atomic.Int32
	var remote cube.Sandbox
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/sandboxes" {
			posts.Add(1)
			var in cube.CreateRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			remote = cube.Sandbox{SandboxID: "vm-retry", TemplateID: in.TemplateID,
				State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: in.Metadata}
			w.WriteHeader(http.StatusCreated)
		} else if r.Method != "GET" || r.URL.Path != "/sandboxes/vm-retry" {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(remote)
	}))
	defer provider.Close()
	c := newAdmissionClient(t, s, provider.URL, 4)
	o, now := storageSetup(t, s)
	o.InnerFreeBytes = 0 // Physical free space can still be hundreds of GiB.
	_, err := c.Create(context.Background(), admissionInput("disk-budget"))
	if !errors.Is(err, cube.ErrStorageUnavailable) || !errors.Is(err, cube.ErrCapacityUnavailable) {
		t.Fatalf("expected retryable capacity refusal, got %v", err)
	}
	var admissions, grants int
	s.db.QueryRow(`SELECT COUNT(*) FROM cube_admission`).Scan(&admissions)
	s.db.QueryRow(`SELECT COUNT(*) FROM cube_storage_grant`).Scan(&grants)
	if posts.Load() != 0 || admissions != 0 || grants != 0 {
		t.Fatalf("refusal left effects: posts=%d admissions=%d grants=%d", posts.Load(), admissions, grants)
	}
	*now = now.Add(2 * time.Second)
	o.Generation++
	o.StartedNS = now.Add(-time.Second).UnixNano()
	o.CompletedNS = now.UnixNano()
	o.InnerFreeBytes = cube.StorageBaseline
	if _, err = c.Create(context.Background(), admissionInput("disk-budget")); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("provider create called %d times", posts.Load())
	}
}
