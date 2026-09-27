package store

import (
	"context"
	"testing"
)

func TestRuntimeAccountingTransitions(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sb := minimalSandbox("01ACCOUNTING00000000000001", "sleep")
	sb.Status = "stopped"
	if err := st.Create(ctx, sb); err != nil {
		t.Fatal(err)
	}
	a, err := st.RuntimeAccounting(ctx, sb.ID)
	if err != nil || a.TotalRunningSeconds != 0 || a.StartedAt != nil {
		t.Fatalf("initial: %+v %v", a, err)
	}
	if err := st.MarkRunning(ctx, sb.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	a, err = st.RuntimeAccounting(ctx, sb.ID)
	if err != nil || a.StartedAt == nil {
		t.Fatalf("wake: %+v %v", a, err)
	}
	// Move only the test clock boundary; repeated running writes must not reset it.
	if _, err := st.DB().Exec(`UPDATE sandbox_runtime_accounting SET running_since=unixepoch()-120 WHERE sandbox_id=?`, sb.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRunning(ctx, sb.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkStopped(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	a, err = st.RuntimeAccounting(ctx, sb.ID)
	if err != nil || a.StartedAt != nil || a.ObservedRunningSince != nil || a.TotalRunningSeconds < 120 || a.TotalRunningSeconds > 122 {
		t.Fatalf("pause: %+v %v", a, err)
	}
	saved := a.TotalRunningSeconds
	if err := st.MarkStopped(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	a, _ = st.RuntimeAccounting(ctx, sb.ID)
	if a.TotalRunningSeconds != saved {
		t.Fatal("double-counted paused time")
	}
	if err := st.MarkRunning(ctx, sb.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE sandbox_runtime_accounting SET start_known=0 WHERE sandbox_id=?`, sb.ID); err != nil {
		t.Fatal(err)
	}
	a, _ = st.RuntimeAccounting(ctx, sb.ID)
	if a.StartedAt != nil || a.ObservedRunningSince == nil {
		t.Fatal("unknown start presented as exact uptime")
	}
}
