package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestCubeStatusReportsAccountingWithoutWaking(t *testing.T) {
	s, connects, calls := cubePreviewFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("guest contacted") }, "paused")
	if err := s.Store.MarkStopped(context.Background(), cubePreviewTestID); err != nil {
		t.Fatal(err)
	}
	path := "/v1/sandboxes/" + cubePreviewTestID
	if w := cubeRequest(s, "GET", path, "", "other-tenant"); w.Code != 404 {
		t.Fatalf("tenant isolation: %d", w.Code)
	}
	w := cubeRequest(s, "GET", path, "", cfgTenant)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var response v1Sandbox
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RuntimeAccounting == nil || response.RuntimeAccounting.StartedAt != nil || response.RuntimeAccounting.TrackingSince == "" {
		t.Fatalf("accounting: %+v", response.RuntimeAccounting)
	}
	if response.Resources == nil || response.Resources.CPUCount != 2 || response.Resources.MemoryMB != 2048 {
		t.Fatalf("resources: %+v", response.Resources)
	}
	if connects.Load() != 0 || calls.Load() != 0 {
		t.Fatal("status read woke/contacted guest")
	}
}
