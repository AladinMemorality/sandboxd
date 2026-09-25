package cube

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// RecoveryCreationStore is an administrator-only offline seam. Implementations
// must persist intent and retain a charged slot BEFORE any provider request.
// It is deliberately not used by the normal tenant Create/Connect APIs.
type RecoveryCreationStore interface {
	AdmissionStore
	CubeRecoveryCreateIntent(context.Context, string, string, string, string, string, string, string) (RecoveryCreateIntent, error)
	CubeRecoveryCreateObserved(context.Context, string, string, *Sandbox) error
	CubeRecoveryCreation(context.Context, string) (RecoveryCreateIntent, error)
}
type RecoveryCreateIntent struct {
	RecoveryID, SandboxID, AppID, OldRuntimeID, TemplateID, OperationToken, RequestSHA256 string
}

// CreateRecovery performs exactly one POST using a previously quarantined slot.
// Ambiguity is retained forever for explicit adoption; this method never retries.
func (c *Client) CreateRecovery(ctx context.Context, recovery RecoveryCreationStore, id string, in CreateRequest) (*Sandbox, error) {
	if recovery == nil || c.admission == nil {
		return nil, errors.New("offline recovery and durable admission required")
	}
	in, err := prepareCreate(in)
	if err != nil {
		return nil, err
	}
	g := c.admission
	if g.store != recovery {
		return nil, errors.New("recovery must share the configured admission store")
	}
	if _, ok := g.config.Templates[in.TemplateID]; !ok || in.Lifecycle.AutoResume {
		return nil, errors.New("reviewed recovery template and disabled auto-resume required")
	}
	if validateID(id) != nil || validateID(in.Metadata["sandboxd_id"]) != nil || validateID(in.Metadata["sandboxd_app_id"]) != nil || in.EnvVars["RUNTIMED_HTTP_TOKEN"] == "" {
		return nil, errors.New("recovery identity or credential missing")
	}
	select {
	case g.createGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-g.createGate }()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	token, err := admissionToken()
	if err != nil {
		return nil, err
	}
	metadata := make(map[string]string, len(in.Metadata)+2)
	for k, v := range in.Metadata {
		metadata[k] = v
	}
	metadata["sandboxd_admission_operation"] = token
	metadata["sandboxd_recovery_id"] = id
	in.Metadata = metadata
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	requestHash := fmt.Sprintf("%x", sha256.Sum256(raw))
	supervisorHash := fmt.Sprintf("%x", sha256.Sum256([]byte(in.EnvVars["RUNTIMED_HTTP_TOKEN"])))
	intent, err := recovery.CubeRecoveryCreateIntent(ctx, id, token, requestHash, supervisorHash, in.TemplateID, in.Metadata["sandboxd_id"], in.Metadata["sandboxd_app_id"])
	if err != nil {
		return nil, err
	}
	// Browser/operator cancellation after the durable intent must not abandon an
	// otherwise acknowledged mutation. True infrastructure ambiguity remains held.
	operation, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*standardTimeout)
	defer stop()
	remote, err := c.createRaw(operation, in)
	if err != nil {
		return nil, fmt.Errorf("%w: recovery create outcome requires operator review", ErrAdmissionPending)
	}
	actual, err := c.getRaw(operation, remote.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("%w: recovery create verification unavailable", ErrAdmissionPending)
	}
	if err = validateRecoveryRemote(g, intent, actual); err != nil {
		return nil, err
	}
	if err = recovery.CubeRecoveryCreateObserved(operation, id, token, actual); err != nil {
		return nil, fmt.Errorf("%w: recovery target acknowledgment failed", ErrAdmissionPending)
	}
	return remote, nil
}
func validateRecoveryRemote(g *admissionGuard, in RecoveryCreateIntent, actual *Sandbox) error {
	if err := g.validRemote(actual); err != nil {
		return fmt.Errorf("%w: recovery resource/state mismatch", ErrAdmissionPending)
	}
	if actual.State != "running" || actual.SandboxID == in.OldRuntimeID || actual.TemplateID != in.TemplateID || actual.Metadata["sandboxd_id"] != in.SandboxID || actual.Metadata["sandboxd_app_id"] != in.AppID || actual.Metadata["sandboxd_admission_operation"] != in.OperationToken || actual.Metadata["sandboxd_recovery_id"] != in.RecoveryID {
		return fmt.Errorf("%w: recovery identity mismatch", ErrAdmissionPending)
	}
	return nil
}

// AdoptRecovery only records an already-created exact target. The offline caller
// must subsequently prove authenticated access with the journaled supervisor
// credential, imports/config/history/readiness, before the binding can commit.
func (c *Client) AdoptRecovery(ctx context.Context, recovery RecoveryCreationStore, id, runtimeID string) (*Sandbox, error) {
	if c.admission == nil || recovery == nil {
		return nil, errors.New("offline recovery and durable admission required")
	}
	if c.admission.store != recovery {
		return nil, errors.New("recovery must share the configured admission store")
	}
	intent, err := recovery.CubeRecoveryCreation(ctx, id)
	if err != nil {
		return nil, err
	}
	actual, err := c.getRaw(ctx, runtimeID)
	if err != nil {
		return nil, err
	}
	if err = validateRecoveryRemote(c.admission, intent, actual); err != nil {
		return nil, err
	}
	if err = recovery.CubeRecoveryCreateObserved(ctx, id, intent.OperationToken, actual); err != nil {
		return nil, err
	}
	return actual, nil
}
