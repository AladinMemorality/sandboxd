package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// CubeRelocation is a trusted operator journal, not a tenant operation. The
// caller proves the native source is paused, exports its complete owner state,
// and verifies the destination before Commit. Quarantine prevents the running
// controller from reopening the source while the operator works through S3.
type CubeRelocation struct {
	ID, SandboxID, AppID, OwnerSHA, SourceRuntimeID string
	TargetWorker, TargetKey, TemplateID, Domain     string
	TargetTemplateID                                string `json:",omitempty"`
	SameProfileReplacement                          bool   `json:",omitempty"`
	ConfigRevision                                  int64
	CredentialSHA, ConfigSHA, TaskSHA               string
	TaskCount                                       int
	SourceAdmission                                 cube.AdmissionRecord
}

// DestinationTemplate preserves legacy journals while allowing an explicitly
// reviewed destination profile without changing the source fingerprint.
func (j CubeRelocation) DestinationTemplate() string {
	if j.TargetTemplateID != "" {
		return j.TargetTemplateID
	}
	return j.TemplateID
}

// RetainedRelocationSources recognizes only completed operator relocations.
// The exact released source reservation and quarantine must still be present;
// native lifecycle observation independently requires each retained VM paused.
func RetainedRelocationSources(ctx context.Context, db *sql.DB, worker string) ([]string, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT baseline_json FROM cube_relocation WHERE phase='complete' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var journals []CubeRelocation
	for rows.Next() {
		var raw string
		var j CubeRelocation
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &j)
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		if j.SourceAdmission.WorkerID == worker {
			journals = append(journals, j)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, j := range journals {
		expected := j.SourceAdmission
		expected.Key = "relocation-retired:" + j.ID
		if expected.RuntimeID != j.SourceRuntimeID || expected.Charged != 0 || expected.State != "released" {
			return nil, ErrConflict
		}
		actual, err := scanAdmission(tx.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged,worker_id FROM cube_admission WHERE admission_key=?`, expected.Key))
		if err != nil {
			return nil, err
		}
		if actual != expected {
			return nil, ErrConflict
		}
		var bound, fenced int
		if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM runtime_binding WHERE runtime_id=?),(SELECT count(*) FROM cube_runtime_quarantine WHERE runtime_id=? AND recovery_id=? AND sandbox_id=?)`, j.SourceRuntimeID, j.SourceRuntimeID, "relocation:"+j.ID, j.SandboxID).Scan(&bound, &fenced); err != nil {
			return nil, err
		}
		if bound != 0 || fenced != 1 {
			return nil, ErrConflict
		}
		ids = append(ids, j.SourceRuntimeID)
	}
	return ids, nil
}

func relocationBinding(ctx context.Context, tx *sql.Tx, sid string) (*RuntimeBinding, string, string, string, error) {
	b := &RuntimeBinding{SandboxID: sid, Provider: "cube"}
	var app, owner, status string
	err := tx.QueryRowContext(ctx, `SELECT s.app_id,a.owner_token,s.status,b.runtime_id,b.template_id,b.domain,b.token_ciphertext,b.token_nonce,b.config_revision,b.config_applied_revision FROM sandbox s JOIN app a ON a.id=s.app_id JOIN runtime_binding b ON b.sandbox_id=s.id WHERE s.id=? AND s.runtime_provider='cube' AND b.provider='cube'`, sid).Scan(&app, &owner, &status, &b.RuntimeID, &b.TemplateID, &b.Domain, &b.TokenCiphertext, &b.TokenNonce, &b.ConfigRevision, &b.ConfigAppliedRevision)
	return b, app, owner, status, err
}

func loadRelocation(ctx context.Context, tx *sql.Tx, id string) (CubeRelocation, string, error) {
	var j CubeRelocation
	var raw, phase string
	err := tx.QueryRowContext(ctx, `SELECT baseline_json,phase FROM cube_relocation WHERE id=?`, id).Scan(&raw, &phase)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &j)
	}
	return j, phase, err
}

func (s *Store) GetCubeRelocation(ctx context.Context, id string) (CubeRelocation, string, error) {
	var j CubeRelocation
	var raw, phase string
	err := s.db.QueryRowContext(ctx, `SELECT baseline_json,phase FROM cube_relocation WHERE id=?`, id).Scan(&raw, &phase)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &j)
	}
	return j, phase, err
}

func relocationUnchanged(ctx context.Context, tx *sql.Tx, j CubeRelocation) error {
	b, app, owner, status, err := relocationBinding(ctx, tx, j.SandboxID)
	if err != nil {
		return err
	}
	if status == "error" {
		// The deployed reconciler reports quarantined runtimes as requiring
		// recovery. Only this journal's exact source fence permits that state.
		var fenced int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cube_runtime_quarantine WHERE runtime_id=? AND sandbox_id=? AND recovery_id=?`, j.SourceRuntimeID, j.SandboxID, "relocation:"+j.ID).Scan(&fenced); err != nil {
			return err
		}
		if fenced != 1 {
			return ErrConflict
		}
	} else if status != "stopped" {
		return ErrConflict
	}
	if app != j.AppID || recoveryHash(owner) != j.OwnerSHA || b.RuntimeID != j.SourceRuntimeID || b.TemplateID != j.TemplateID || b.Domain != j.Domain || b.ConfigRevision != j.ConfigRevision || CubeRecoveryCredentialSHA(b.TokenCiphertext, b.TokenNonce) != j.CredentialSHA {
		return ErrConflict
	}
	fp, err := recoveryConfigFingerprint(ctx, tx, app)
	if err != nil {
		return err
	}
	th, count, err := recoveryTaskHash(ctx, tx, j.SandboxID)
	if err != nil {
		return err
	}
	if fp != j.ConfigSHA || th != j.TaskSHA || count != j.TaskCount {
		return ErrConflict
	}
	var running int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM task WHERE sandbox_id=? AND status IN ('running','queued')`, j.SandboxID).Scan(&running); err != nil {
		return err
	}
	if running != 0 {
		return errors.New("active task prevents relocation")
	}
	return nil
}

func (s *Store) BeginCubeRelocation(ctx context.Context, id, sid, expectedRuntime, targetWorker string, targetTemplate ...string) (CubeRelocation, error) {
	return s.beginCubeRelocation(ctx, id, sid, expectedRuntime, targetWorker, false, targetTemplate...)
}

// BeginCubeSameProfileReplacement is an explicit operator-only replacement of a
// paused runtime. The caller must retain its source disk and verify a complete
// source export; the normal relocation and commit fencing remains mandatory.
func (s *Store) BeginCubeSameProfileReplacement(ctx context.Context, id, sid, expectedRuntime, targetWorker, targetTemplate string) (CubeRelocation, error) {
	return s.beginCubeRelocation(ctx, id, sid, expectedRuntime, targetWorker, true, targetTemplate)
}

func (s *Store) beginCubeRelocation(ctx context.Context, id, sid, expectedRuntime, targetWorker string, sameProfileReplacement bool, targetTemplate ...string) (CubeRelocation, error) {
	var out CubeRelocation
	destination := ""
	if len(targetTemplate) > 1 {
		return out, errors.New("one destination template required")
	}
	if len(targetTemplate) == 1 {
		destination = targetTemplate[0]
		if !projectID.MatchString(destination) {
			return out, errors.New("invalid destination template")
		}
	}
	if !projectID.MatchString(id) || !projectID.MatchString(sid) || !projectID.MatchString(expectedRuntime) || !projectID.MatchString(targetWorker) {
		return out, errors.New("invalid relocation identity")
	}
	err := s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		if j, phase, e := loadRelocation(ctx, tx, id); e == nil {
			if phase != "fenced" || j.SandboxID != sid || j.SourceRuntimeID != expectedRuntime || j.TargetWorker != targetWorker || j.TargetTemplateID != destination || j.SameProfileReplacement != sameProfileReplacement {
				return ErrConflict
			}
			out = j
			return relocationUnchanged(ctx, tx, j)
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		b, app, owner, status, e := relocationBinding(ctx, tx, sid)
		if e != nil {
			return e
		}
		if status != "stopped" || b.RuntimeID != expectedRuntime {
			return ErrConflict
		}
		old, e := scanAdmission(tx.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged,worker_id FROM cube_admission WHERE admission_key=?`, "app:"+app))
		if e != nil {
			return e
		}
		if old.RuntimeID != expectedRuntime || old.TemplateID != b.TemplateID || old.State != "released" || old.Charged != 0 {
			return cube.ErrAdmissionPending
		}
		// Ordinary same-worker moves still require an explicit profile change.
		// Replacement is a distinct, durable operator intent and cannot also
		// change worker or profile. It never replays a failed native resume.
		if sameProfileReplacement {
			if old.WorkerID != targetWorker || destination != b.TemplateID {
				return ErrConflict
			}
		} else if old.WorkerID == targetWorker && (destination == "" || destination == b.TemplateID) {
			return cube.ErrAdmissionPending
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM cube_recovery WHERE sandbox_id=? AND phase<>'complete'`, sid).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return ErrConflict
		}
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM cube_admission_policy p JOIN cube_worker_identity i ON i.worker_id=p.worker_id WHERE p.worker_id=?`, targetWorker).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return errors.New("target worker not enrolled")
		}
		out = CubeRelocation{ID: id, SandboxID: sid, AppID: app, OwnerSHA: recoveryHash(owner), SourceRuntimeID: expectedRuntime, TargetWorker: targetWorker, TargetKey: "relocation:" + id, TemplateID: b.TemplateID, Domain: b.Domain, ConfigRevision: b.ConfigRevision, CredentialSHA: CubeRecoveryCredentialSHA(b.TokenCiphertext, b.TokenNonce), SourceAdmission: old}
		out.TargetTemplateID = destination
		out.SameProfileReplacement = sameProfileReplacement
		if destination != "" {
			var raw string
			if e = tx.QueryRowContext(ctx, `SELECT contract FROM cube_resource_budget WHERE worker_id=?`, targetWorker).Scan(&raw); e != nil {
				return e
			}
			var contract resourceContract
			if e = json.Unmarshal([]byte(raw), &contract); e != nil {
				return e
			}
			if _, ok := contract.Templates[destination]; !ok {
				return errors.New("destination template has no durable resource contract")
			}
			if profile, ok := contract.Budget.Profiles[destination]; !ok || profile.Kind != "runtime" {
				return errors.New("destination must be a runtime profile")
			}
		}
		out.ConfigSHA, e = recoveryConfigFingerprint(ctx, tx, app)
		if e != nil {
			return e
		}
		out.TaskSHA, out.TaskCount, e = recoveryTaskHash(ctx, tx, sid)
		if e != nil {
			return e
		}
		if e = relocationUnchanged(ctx, tx, out); e != nil {
			return e
		}
		raw, e := json.Marshal(out)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO cube_relocation(id,sandbox_id,phase,baseline_json) VALUES(?,?,'fenced',?)`, id, sid, string(raw)); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO cube_runtime_quarantine(runtime_id,recovery_id,sandbox_id,created_at) VALUES(?,?,?,unixepoch())`, expectedRuntime, "relocation:"+id, sid)
		return e
	})
	return out, err
}

// Commit switches routing and admission in one transaction. Target metadata and
// complete workspace/home/history/config/readiness evidence are checked by the
// trusted coordinator, whose immutable receipt hash is retained here. No source
// VM is deleted; its provider ID remains quarantined for rollback review.
func (s *Store) CommitCubeRelocation(ctx context.Context, id, targetToken, evidenceSHA string, b RuntimeBinding) error {
	if !validRecoverySHA(evidenceSHA) || b.Provider != "cube" || !projectID.MatchString(b.RuntimeID) || len(b.TokenCiphertext) == 0 || len(b.TokenNonce) == 0 {
		return errors.New("verified target binding required")
	}
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, phase, e := loadRelocation(ctx, tx, id)
		if e != nil {
			return e
		}
		if phase != "fenced" {
			return ErrConflict
		}
		if e = relocationUnchanged(ctx, tx, j); e != nil {
			return e
		}
		if b.SandboxID != j.SandboxID || b.RuntimeID == j.SourceRuntimeID || b.TemplateID != j.DestinationTemplate() || b.Domain != j.Domain || b.ConfigRevision != j.ConfigRevision || b.ConfigAppliedRevision != j.ConfigRevision {
			return ErrConflict
		}
		old, e := scanAdmission(tx.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged,worker_id FROM cube_admission WHERE admission_key=?`, "app:"+j.AppID))
		if e != nil {
			return e
		}
		if old != j.SourceAdmission {
			return ErrConflict
		}
		target, e := scanAdmission(tx.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged,worker_id FROM cube_admission WHERE admission_key=?`, j.TargetKey))
		if e != nil {
			return e
		}
		if target.RuntimeID != b.RuntimeID || target.TemplateID != j.DestinationTemplate() || target.WorkerID != j.TargetWorker || target.State != "active" || target.Charged != 1 || target.Token != targetToken {
			return cube.ErrAdmissionPending
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM cube_runtime_quarantine WHERE runtime_id=? AND sandbox_id=? AND recovery_id=?`, j.SourceRuntimeID, j.SandboxID, "relocation:"+id).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM runtime_binding WHERE runtime_id=?)+(SELECT count(*) FROM cube_runtime_quarantine WHERE runtime_id=?)`, b.RuntimeID, b.RuntimeID).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return ErrConflict
		}
		appKey := "app:" + j.AppID
		retiredKey := "relocation-retired:" + id
		if _, e = tx.ExecContext(ctx, `UPDATE cube_admission SET admission_key=? WHERE admission_key=?`, retiredKey, appKey); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE cube_storage_grant SET admission_key=? WHERE admission_key=? AND worker_id=?`, retiredKey, appKey, old.WorkerID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE cube_admission SET admission_key=? WHERE admission_key=?`, appKey, j.TargetKey); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE cube_storage_grant SET admission_key=? WHERE admission_key=? AND worker_id=?`, appKey, j.TargetKey, j.TargetWorker); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE runtime_binding SET runtime_id=?,template_id=?,token_ciphertext=?,token_nonce=?,config_applied_revision=? WHERE sandbox_id=?`, b.RuntimeID, b.TemplateID, b.TokenCiphertext, b.TokenNonce, b.ConfigAppliedRevision, j.SandboxID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE sandbox SET status='stopped',error_message=NULL WHERE id=?`, j.SandboxID); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_relocation SET phase='complete',target_runtime_id=?,verification_sha256=?,updated_at=unixepoch() WHERE id=?`, b.RuntimeID, evidenceSHA, id)
		return e
	})
}

// Abort is available only after the target reservation is authoritatively
// deleted (or was never made). It exposes the unchanged, stopped source again.
func (s *Store) AbortCubeRelocation(ctx context.Context, id string) error {
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, phase, e := loadRelocation(ctx, tx, id)
		if e != nil {
			return e
		}
		if phase != "fenced" {
			return ErrConflict
		}
		if e = relocationUnchanged(ctx, tx, j); e != nil {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM cube_admission WHERE admission_key=? AND (state<>'deleted' OR charged<>0)`, j.TargetKey).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return cube.ErrAdmissionPending
		}
		r, e := tx.ExecContext(ctx, `DELETE FROM cube_runtime_quarantine WHERE runtime_id=? AND sandbox_id=? AND recovery_id=?`, j.SourceRuntimeID, j.SandboxID, "relocation:"+id)
		if e != nil {
			return e
		}
		if n, e := r.RowsAffected(); e != nil || n != 1 {
			return ErrConflict
		}
		if _, e = tx.ExecContext(ctx, `UPDATE sandbox SET status='stopped',error_message=NULL WHERE id=?`, j.SandboxID); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_relocation SET phase='aborted',updated_at=unixepoch() WHERE id=?`, id)
		return e
	})
}
