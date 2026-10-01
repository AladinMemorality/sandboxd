package cube

import (
	"context"
	"errors"
	"testing"
)

func TestRecoveryRequiresAuthoritativePinnedPlacement(t *testing.T) {
	c := &Client{admission: &admissionGuard{config: AdmissionConfig{NodeID: "10.0.2.15", CPUCount: 2, MemoryMB: 2048, Templates: map[string]AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}}}}
	in := RecoveryCreateIntent{RecoveryID: "recover", SandboxID: "stable", AppID: "app", OldRuntimeID: "old", TemplateID: "tpl-reviewed", OperationToken: "operation"}
	actual := &Sandbox{SandboxID: "replacement", TemplateID: in.TemplateID, State: "running", CPUCount: 2, MemoryMB: 2048, Metadata: map[string]string{"sandboxd_id": in.SandboxID, "sandboxd_app_id": in.AppID, "sandboxd_recovery_id": in.RecoveryID, "sandboxd_admission_operation": in.OperationToken}}
	if e := c.validateRecoveryRemote(context.Background(), in, actual); !errors.Is(e, ErrAdmissionPending) {
		t.Fatal("missing placement accepted", e)
	}
	c.placement = func(ctx context.Context, id, node string) error {
		if id != "replacement" || node != "10.0.2.15" {
			t.Fatal("wrong placement lookup")
		}
		return errors.New("wrong worker")
	}
	if e := c.validateRecoveryRemote(context.Background(), in, actual); !errors.Is(e, ErrAdmissionPending) {
		t.Fatal("wrong placement accepted", e)
	}
	c.placement = func(context.Context, string, string) error { return nil }
	if e := c.validateRecoveryRemote(context.Background(), in, actual); e != nil {
		t.Fatal(e)
	}
}
