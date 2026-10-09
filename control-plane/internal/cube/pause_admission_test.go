package cube

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type pauseLedger struct {
	AdmissionStore
	record AdmissionRecord
}

func (l *pauseLedger) AdmissionLookup(context.Context, string) (AdmissionRecord, error) {
	return l.record, nil
}
func (l *pauseLedger) AdmissionObserveReleased(_ context.Context, old AdmissionRecord, deleted bool) error {
	if old.State != "active" || deleted {
		return errors.New("unexpected release")
	}
	l.record.State = "released"
	l.record.Charged = 0
	return nil
}
func TestPausedRuntimeStopRequiresReleasedAdmission(t *testing.T) {
	for _, tc := range []struct {
		state   string
		charged int
		pending bool
	}{
		{"released", 0, false}, {"active", 1, false}, {"pending", 1, true}, {"released", 1, true},
	} {
		t.Run(tc.state+string(rune('0'+tc.charged)), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected provider mutation %s", r.Method)
				w.WriteHeader(500)
			})
			ledger := &pauseLedger{record: AdmissionRecord{Key: "owned", RuntimeID: "vm-one", State: tc.state, Charged: tc.charged}}
			c.admission = &admissionGuard{store: ledger, config: AdmissionConfig{NodeID: "node-one", Templates: map[string]AdmissionResources{"tpl-one": {CPUCount: 2, MemoryMB: 2048}}}}
			c.observation = func(context.Context, string) (*Sandbox, error) {
				return &Sandbox{SandboxID: "vm-one", TemplateID: "tpl-one", ClientID: "node-one", State: "paused", CPUCount: 2, MemoryMB: 2048}, nil
			}
			err := c.Pause(context.Background(), "vm-one")
			if tc.pending {
				if !errors.Is(err, ErrAdmissionPending) {
					t.Fatal("unsettled stop accepted", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.pending && ledger.record.State != tc.state {
				t.Fatal("pending operation was rewritten")
			}
		})
	}
}
