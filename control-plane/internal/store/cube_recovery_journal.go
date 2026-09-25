package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// These primitives must only be called by an exclusive offline recovery session.
// Provider-stop/import evidence is a trusted operator attestation, not something
// a SQLite transaction can independently establish about another machine.
type CubeRecoveryPlan struct {
	ID, SandboxID, ExpectedRuntimeID, TargetTemplateID, TargetDomain string
	ExpectedConfigRevision                                           int64
	ArtifactPaths                                                    map[string]string `json:"-"`
	Artifacts                                                        map[string]string // reviewed role -> SHA256, no credential contents
	SupervisorSHA256                                                 string
	PlannedCiphertext, PlannedNonce                                  []byte `json:"-"`
}
type CubeRecoveryJournal struct {
	ID, SandboxID, AppID, Phase                           string
	OwnerToken                                            string         `json:"-"`
	Old                                                   RuntimeBinding `json:"-"`
	Target                                                RuntimeBinding `json:"-"`
	ConfigFingerprint, TaskFingerprint                    string
	TaskCount                                             int
	ArtifactPaths                                         map[string]string `json:"-"`
	Artifacts                                             map[string]string `json:"-"`
	ArtifactsSHA256, SupervisorSHA256                     string
	PlannedCiphertext, PlannedNonce                       []byte               `json:"-"`
	OldAdmission                                          cube.AdmissionRecord `json:"-"`
	HoldToken, FenceSHA256, OperationToken, RequestSHA256 string               `json:"-"`
	Verification                                          string               `json:"-"`
}
type CubeRecoveryFence struct {
	RecoveryID, OldRuntimeID, ArtifactsSHA256, EvidenceSHA256 string
	OldExecutionStopped, ProviderRequestsDrained              bool
}
type CubeRecoveryVerification struct {
	AdmissionToken                                                                                   string
	RecoveryID, SandboxID, AppID, OldRuntimeID, NewRuntimeID, TemplateID                             string
	ArtifactsSHA256, ConfigFingerprint, TaskFingerprint, CredentialSHA256                            string
	EvidenceSHA256, WorkspaceSHA256, HomeSHA256, HistorySHA256                                       string
	ConfigRevision                                                                                   int64
	Authenticated, WorkspaceVerified, HomeVerified, HistoryVerified, ConfigApplied, ApplicationReady bool
}

func validRecoverySHA(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}
func recoveryHash(value any) string {
	b, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func CubeRecoveryCredentialSHA(ciphertext, nonce []byte) string {
	return recoveryHash([][]byte{ciphertext, nonce})
}

var recoveryDomain = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func recoveryID(s string) bool {
	return len(s) > 0 && len(s) <= 128 && !strings.ContainsAny(s, " /\\\x00\r\n\t")
}
func recoveryTaskHash(ctx context.Context, tx *sql.Tx, id string) (string, int, error) {
	rows, e := tx.QueryContext(ctx, `SELECT task_id,agent,status,created_at,COALESCE(finished_at,0),COALESCE(result_json,'') FROM task WHERE sandbox_id=? ORDER BY task_id`, id)
	if e != nil {
		return "", 0, e
	}
	defer rows.Close()
	h := sha256.New()
	n := 0
	for rows.Next() {
		var task, agent, status, result string
		var started, finished int64
		if e = rows.Scan(&task, &agent, &status, &started, &finished, &result); e != nil {
			return "", 0, e
		}
		b, _ := json.Marshal([]any{task, agent, status, started, finished, result})
		h.Write(b)
		n++
	}
	return fmt.Sprintf("%x", h.Sum(nil)), n, rows.Err()
}

// Recovery freezes every app config policy, including broker-only credentials.
// Unlike ordinary runtime recreation, it must not accept concurrent owner edits
// merely because those edits are not injected into the guest process.
func recoveryConfigFingerprint(ctx context.Context, tx *sql.Tx, app string) (string, error) {
	var preset sql.NullString
	if e := tx.QueryRowContext(ctx, `SELECT runtime_preset FROM app WHERE id=?`, app).Scan(&preset); e != nil {
		return "", e
	}
	rows, e := tx.QueryContext(ctx, `SELECT key,access_policy,value_ciphertext,value_nonce,value_plaintext,sensitive FROM app_config WHERE app_id=? ORDER BY key`, app)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	h := sha256.New()
	b, _ := json.Marshal(preset)
	h.Write(b)
	for rows.Next() {
		var key, policy string
		var cipher, nonce []byte
		var plain sql.NullString
		var sensitive int
		if e = rows.Scan(&key, &policy, &cipher, &nonce, &plain, &sensitive); e != nil {
			return "", e
		}
		b, _ := json.Marshal([]any{key, policy, cipher, nonce, plain, sensitive})
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), rows.Err()
}

const recoveryColumns = `recovery_id,sandbox_id,app_id,owner_token,phase,old_runtime_id,old_template_id,old_domain,old_token_ciphertext,old_token_nonce,config_revision,config_applied_revision,config_fingerprint,task_fingerprint,task_count,source_artifacts_json,source_artifact_paths_json,artifacts_sha256,target_template_id,target_domain,supervisor_sha256,planned_token_ciphertext,planned_token_nonce,old_admission_json,hold_token,fence_sha256,operation_token,request_sha256,new_runtime_id,new_token_ciphertext,new_token_nonce,verification_json`

func scanRecovery(row *sql.Row) (*CubeRecoveryJournal, error) {
	j := &CubeRecoveryJournal{}
	var artifacts, paths, admission string
	e := row.Scan(&j.ID, &j.SandboxID, &j.AppID, &j.OwnerToken, &j.Phase, &j.Old.RuntimeID, &j.Old.TemplateID, &j.Old.Domain, &j.Old.TokenCiphertext, &j.Old.TokenNonce, &j.Old.ConfigRevision, &j.Old.ConfigAppliedRevision, &j.ConfigFingerprint, &j.TaskFingerprint, &j.TaskCount, &artifacts, &paths, &j.ArtifactsSHA256, &j.Target.TemplateID, &j.Target.Domain, &j.SupervisorSHA256, &j.PlannedCiphertext, &j.PlannedNonce, &admission, &j.HoldToken, &j.FenceSHA256, &j.OperationToken, &j.RequestSHA256, &j.Target.RuntimeID, &j.Target.TokenCiphertext, &j.Target.TokenNonce, &j.Verification)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if json.Unmarshal([]byte(artifacts), &j.Artifacts) != nil || json.Unmarshal([]byte(paths), &j.ArtifactPaths) != nil || json.Unmarshal([]byte(admission), &j.OldAdmission) != nil {
		return nil, errors.New("invalid recovery journal")
	}
	j.Old.Provider = "cube"
	j.Old.SandboxID = j.SandboxID
	j.Target.Provider = "cube"
	j.Target.SandboxID = j.SandboxID
	j.Target.ConfigRevision = j.Old.ConfigRevision
	return j, nil
}
func (s *Store) GetCubeRecovery(ctx context.Context, id string) (*CubeRecoveryJournal, error) {
	return scanRecovery(s.db.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM cube_recovery WHERE recovery_id=?`, id))
}
func (s *Store) HasIncompleteCubeRecoveries(ctx context.Context) (bool, error) {
	var n int
	e := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_recovery WHERE phase<>'complete'`).Scan(&n)
	return n > 0, e
}
func (s *Store) recoveryWrite(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.submit(ctx, func(db *sql.DB) error {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		r, e := tx.ExecContext(ctx, `UPDATE cube_admission_policy SET max_active=max_active WHERE singleton=1`)
		if e != nil {
			return e
		}
		if n, e := r.RowsAffected(); e != nil || n != 1 {
			return errors.New("durable admission policy required")
		}
		if e = fn(tx); e != nil {
			return e
		}
		return tx.Commit()
	})
}
func recoveryFrozen(ctx context.Context, tx *sql.Tx, j *CubeRecoveryJournal) error {
	var active int
	if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task WHERE status='running'`).Scan(&active); e != nil {
		return e
	}
	if active != 0 {
		return errors.New("active tasks prevent offline recovery")
	}
	var runtime, app, owner, provider string
	var rev int64
	var cipher, nonce []byte
	e := tx.QueryRowContext(ctx, `SELECT b.runtime_id,s.app_id,a.owner_token,s.runtime_provider,b.config_revision,b.token_ciphertext,b.token_nonce FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id JOIN app a ON a.id=s.app_id WHERE b.sandbox_id=?`, j.SandboxID).Scan(&runtime, &app, &owner, &provider, &rev, &cipher, &nonce)
	if e != nil {
		return e
	}
	if runtime != j.Old.RuntimeID || app != j.AppID || owner != j.OwnerToken || provider != "cube" || rev != j.Old.ConfigRevision || !bytes.Equal(cipher, j.Old.TokenCiphertext) || !bytes.Equal(nonce, j.Old.TokenNonce) {
		return ErrConflict
	}
	fp, e := recoveryConfigFingerprint(ctx, tx, j.AppID)
	if e != nil {
		return e
	}
	th, count, e := recoveryTaskHash(ctx, tx, j.SandboxID)
	if e != nil {
		return e
	}
	if fp != j.ConfigFingerprint || th != j.TaskFingerprint || count != j.TaskCount {
		return ErrConflict
	}
	return nil
}
func (s *Store) BeginCubeRecovery(ctx context.Context, p CubeRecoveryPlan) error {
	if !recoveryID(p.ID) || !recoveryID(p.SandboxID) || !recoveryID(p.ExpectedRuntimeID) || !recoveryID(p.TargetTemplateID) || (len(p.TargetDomain) > 253 || !recoveryDomain.MatchString(p.TargetDomain)) || !validRecoverySHA(p.SupervisorSHA256) || len(p.PlannedCiphertext) == 0 || len(p.PlannedNonce) == 0 || len(p.Artifacts) > 16 {
		return errors.New("incomplete bounded recovery plan")
	}
	for _, role := range []string{"native_backup", "controller_backup", "workspace", "home"} {
		if !validRecoverySHA(p.Artifacts[role]) {
			return errors.New("verified recovery backup/artifact hashes required")
		}
	}
	if len(p.ArtifactPaths) != len(p.Artifacts) {
		return errors.New("every recovery artifact requires its retained absolute path")
	}
	for role, hash := range p.Artifacts {
		path := p.ArtifactPaths[role]
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return errors.New("invalid recovery artifact path")
		}
		if !recoveryID(role) || !validRecoverySHA(hash) {
			return errors.New("invalid artifact manifest")
		}
	}
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j := &CubeRecoveryJournal{ID: p.ID, SandboxID: p.SandboxID}
		j.Old.SandboxID = p.SandboxID
		e := tx.QueryRowContext(ctx, `SELECT s.app_id,a.owner_token,b.provider,b.runtime_id,b.template_id,b.domain,b.token_ciphertext,b.token_nonce,b.config_revision,b.config_applied_revision FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id JOIN app a ON a.id=s.app_id WHERE b.sandbox_id=? AND s.runtime_provider='cube'`, p.SandboxID).Scan(&j.AppID, &j.OwnerToken, &j.Old.Provider, &j.Old.RuntimeID, &j.Old.TemplateID, &j.Old.Domain, &j.Old.TokenCiphertext, &j.Old.TokenNonce, &j.Old.ConfigRevision, &j.Old.ConfigAppliedRevision)
		if e != nil {
			return e
		}
		if j.Old.RuntimeID != p.ExpectedRuntimeID || j.Old.ConfigRevision != p.ExpectedConfigRevision || j.Old.Domain != p.TargetDomain {
			return ErrConflict
		}
		var current string
		if e = tx.QueryRowContext(ctx, `SELECT id FROM sandbox WHERE app_id=? ORDER BY created_at DESC LIMIT 1`, j.AppID).Scan(&current); e != nil {
			return e
		}
		if current != p.SandboxID {
			return ErrConflict
		}
		j.ConfigFingerprint, e = recoveryConfigFingerprint(ctx, tx, j.AppID)
		if e != nil {
			return e
		}
		j.TaskFingerprint, j.TaskCount, e = recoveryTaskHash(ctx, tx, j.SandboxID)
		if e != nil {
			return e
		}
		if j.TaskCount > 0 && !validRecoverySHA(p.Artifacts["history"]) {
			return errors.New("canonical task history archive required before recovery")
		}
		if e = recoveryFrozen(ctx, tx, j); e != nil {
			return e
		}
		old, e := scanAdmission(tx.QueryRowContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,token,state,charged FROM cube_admission WHERE admission_key=?`, "app:"+j.AppID))
		if e != nil {
			return e
		}
		if old.RuntimeID != j.Old.RuntimeID || old.TemplateID != j.Old.TemplateID || old.State == "pending" || old.State == "deleted" {
			return cube.ErrAdmissionPending
		}
		// Hold a slot even when a prior authoritative pause had released it. A full
		// crashed active fleet retains its existing charges, never needs an extra slot.
		if old.Charged == 0 {
			var used, max int
			if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(charged),0),(SELECT max_active FROM cube_admission_policy WHERE singleton=1) FROM cube_admission`).Scan(&used, &max); e != nil {
				return e
			}
			if used >= max {
				return cube.ErrCapacityUnavailable
			}
		}
		artifactRaw, _ := json.Marshal(p.Artifacts)
		pathsRaw, _ := json.Marshal(p.ArtifactPaths)
		oldRaw, _ := json.Marshal(old)
		hold := "recovery:" + p.ID
		_, e = tx.ExecContext(ctx, `INSERT INTO cube_recovery(recovery_id,sandbox_id,app_id,owner_token,phase,old_runtime_id,old_template_id,old_domain,old_token_ciphertext,old_token_nonce,config_revision,config_applied_revision,config_fingerprint,task_fingerprint,task_count,source_artifacts_json,source_artifact_paths_json,artifacts_sha256,target_template_id,target_domain,supervisor_sha256,planned_token_ciphertext,planned_token_nonce,old_admission_json,hold_token,updated_at) VALUES(?,?,?,?,'held',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.SandboxID, j.AppID, j.OwnerToken, j.Old.RuntimeID, j.Old.TemplateID, j.Old.Domain, j.Old.TokenCiphertext, j.Old.TokenNonce, j.Old.ConfigRevision, j.Old.ConfigAppliedRevision, j.ConfigFingerprint, j.TaskFingerprint, j.TaskCount, string(artifactRaw), string(pathsRaw), recoveryHash([]any{p.Artifacts, p.ArtifactPaths}), p.TargetTemplateID, p.TargetDomain, p.SupervisorSHA256, p.PlannedCiphertext, p.PlannedNonce, string(oldRaw), hold, time.Now().Unix())
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO cube_runtime_quarantine(runtime_id,recovery_id,sandbox_id,created_at) VALUES(?,?,?,?)`, j.Old.RuntimeID, p.ID, p.SandboxID, time.Now().Unix()); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_admission SET state='pending',operation='recovery_hold',token=?,charged=1 WHERE admission_key=? AND token=?`, hold, old.Key, old.Token)
		return e
	})
}
func (s *Store) RecordCubeRecoveryFence(ctx context.Context, f CubeRecoveryFence) error {
	if !f.OldExecutionStopped || !f.ProviderRequestsDrained || !validRecoverySHA(f.EvidenceSHA256) {
		return errors.New("explicit old-execution and provider-drain evidence required")
	}
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, e := scanRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM cube_recovery WHERE recovery_id=?`, f.RecoveryID))
		if e != nil {
			return e
		}
		if j.Old.RuntimeID != f.OldRuntimeID || j.ArtifactsSHA256 != f.ArtifactsSHA256 {
			return ErrConflict
		}
		if e = recoveryFrozen(ctx, tx, j); e != nil {
			return e
		}
		if j.Phase == "fenced" && j.FenceSHA256 == recoveryHash(f) {
			return nil
		}
		if j.Phase != "held" {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_recovery SET phase='fenced',fence_sha256=?,updated_at=? WHERE recovery_id=?`, recoveryHash(f), time.Now().Unix(), j.ID)
		return e
	})
}
func (s *Store) CubeRecoveryCreation(ctx context.Context, id string) (cube.RecoveryCreateIntent, error) {
	j, e := s.GetCubeRecovery(ctx, id)
	if e != nil {
		return cube.RecoveryCreateIntent{}, e
	}
	if j.Phase != "creating" && j.Phase != "created" {
		return cube.RecoveryCreateIntent{}, cube.ErrAdmissionPending
	}
	return cube.RecoveryCreateIntent{RecoveryID: j.ID, SandboxID: j.SandboxID, AppID: j.AppID, OldRuntimeID: j.Old.RuntimeID, TemplateID: j.Target.TemplateID, OperationToken: j.OperationToken, RequestSHA256: j.RequestSHA256}, nil
}
func (s *Store) CubeRecoveryCreateIntent(ctx context.Context, id, token, requestHash, supervisorHash, template, sandbox, app string) (cube.RecoveryCreateIntent, error) {
	var out cube.RecoveryCreateIntent
	if !validRecoverySHA(requestHash) || !validRecoverySHA(supervisorHash) || !recoveryID(token) {
		return out, errors.New("invalid creation intent")
	}
	e := s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, e := scanRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM cube_recovery WHERE recovery_id=?`, id))
		if e != nil {
			return e
		}
		if j.Phase != "fenced" {
			return cube.ErrAdmissionPending
		}
		if j.Target.TemplateID != template || j.SupervisorSHA256 != supervisorHash || j.SandboxID != sandbox || j.AppID != app {
			return ErrConflict
		}
		if e = recoveryFrozen(ctx, tx, j); e != nil {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE state='pending' AND operation='create'`).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return cube.ErrCreationBusy
		}
		r, e := tx.ExecContext(ctx, `UPDATE cube_admission SET runtime_id='',template_id=?,operation='create',token=? WHERE admission_key=? AND state='pending' AND operation='recovery_hold' AND token=? AND charged=1`, template, token, "app:"+j.AppID, j.HoldToken)
		if e != nil {
			return e
		}
		if n, e := r.RowsAffected(); e != nil || n != 1 {
			return cube.ErrAdmissionPending
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_recovery SET phase='creating',operation_token=?,request_sha256=?,updated_at=? WHERE recovery_id=?`, token, requestHash, time.Now().Unix(), id)
		if e != nil {
			return e
		}
		out = cube.RecoveryCreateIntent{RecoveryID: id, SandboxID: sandbox, AppID: app, OldRuntimeID: j.Old.RuntimeID, TemplateID: template, OperationToken: token, RequestSHA256: requestHash}
		return nil
	})
	return out, e
}
func (s *Store) CubeRecoveryCreateObserved(ctx context.Context, id, token string, actual *cube.Sandbox) error {
	if actual == nil || !recoveryID(actual.SandboxID) || actual.State != "running" || actual.CPUCount != 2 || actual.MemoryMB != 2048 {
		return errors.New("authoritative reviewed recovery allocation required")
	}
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, e := scanRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM cube_recovery WHERE recovery_id=?`, id))
		if e != nil {
			return e
		}
		if (j.Phase != "creating" && j.Phase != "created") || j.OperationToken != token || actual.SandboxID == j.Old.RuntimeID || actual.TemplateID != j.Target.TemplateID || (actual.Domain != "" && actual.Domain != j.Target.Domain) || actual.Metadata["sandboxd_id"] != j.SandboxID || actual.Metadata["sandboxd_app_id"] != j.AppID || actual.Metadata["sandboxd_admission_operation"] != token || actual.Metadata["sandboxd_recovery_id"] != j.ID {
			return ErrConflict
		}
		if e = recoveryFrozen(ctx, tx, j); e != nil {
			return e
		}
		if j.Phase == "created" {
			if j.Target.RuntimeID == actual.SandboxID {
				return nil
			}
			return ErrConflict
		}
		var occupied int
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM cube_runtime_quarantine WHERE runtime_id=?)+(SELECT COUNT(*) FROM runtime_binding WHERE runtime_id=?)`, actual.SandboxID, actual.SandboxID).Scan(&occupied); e != nil {
			return e
		}
		if occupied != 0 {
			return ErrConflict
		}
		r, e := tx.ExecContext(ctx, `UPDATE cube_admission SET runtime_id=?,state='active' WHERE admission_key=? AND state='pending' AND operation='create' AND token=? AND charged=1`, actual.SandboxID, "app:"+j.AppID, token)
		if e != nil {
			return e
		}
		if n, e := r.RowsAffected(); e != nil || n != 1 {
			return cube.ErrAdmissionPending
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_recovery SET phase='created',new_runtime_id=?,updated_at=? WHERE recovery_id=?`, actual.SandboxID, time.Now().Unix(), id)
		return e
	})
}
func (s *Store) RecordCubeRecoveryCredential(ctx context.Context, id, runtime string, cipher, nonce []byte) error {
	if len(cipher) == 0 || len(nonce) == 0 {
		return errors.New("encrypted replacement credentials required")
	}
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, e := scanRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM cube_recovery WHERE recovery_id=?`, id))
		if e != nil {
			return e
		}
		if j.Phase != "created" || j.Target.RuntimeID != runtime {
			return ErrConflict
		}
		if e = recoveryFrozen(ctx, tx, j); e != nil {
			return e
		}
		if len(j.Target.TokenCiphertext) > 0 {
			if bytes.Equal(j.Target.TokenCiphertext, cipher) && bytes.Equal(j.Target.TokenNonce, nonce) {
				return nil
			}
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_recovery SET new_token_ciphertext=?,new_token_nonce=?,updated_at=? WHERE recovery_id=?`, cipher, nonce, time.Now().Unix(), id)
		return e
	})
}
func validateRecoveryVerification(j *CubeRecoveryJournal, v CubeRecoveryVerification) error {
	if !v.Authenticated || !v.WorkspaceVerified || !v.HomeVerified || !v.HistoryVerified || !v.ConfigApplied || !v.ApplicationReady || !validRecoverySHA(v.EvidenceSHA256) || !validRecoverySHA(v.HistorySHA256) || !recoveryID(v.AdmissionToken) {
		return errors.New("complete authenticated workspace/home/history/config/readiness evidence required")
	}
	if v.RecoveryID != j.ID || v.SandboxID != j.SandboxID || v.AppID != j.AppID || v.OldRuntimeID != j.Old.RuntimeID || v.NewRuntimeID != j.Target.RuntimeID || v.TemplateID != j.Target.TemplateID || v.ArtifactsSHA256 != j.ArtifactsSHA256 || v.ConfigFingerprint != j.ConfigFingerprint || v.TaskFingerprint != j.TaskFingerprint || v.ConfigRevision != j.Old.ConfigRevision || len(j.Target.TokenCiphertext) == 0 || len(j.Target.TokenNonce) == 0 || v.CredentialSHA256 != CubeRecoveryCredentialSHA(j.Target.TokenCiphertext, j.Target.TokenNonce) || v.WorkspaceSHA256 != j.Artifacts["workspace"] || v.HomeSHA256 != j.Artifacts["home"] {
		return ErrConflict
	}
	if (j.TaskCount > 0 && v.HistorySHA256 != j.Artifacts["history"]) || (j.TaskCount == 0 && v.HistorySHA256 != j.TaskFingerprint) {
		return errors.New("canonical recovered task history archive is missing or mismatched")
	}
	return nil
}
func (s *Store) VerifyCubeRecovery(ctx context.Context, v CubeRecoveryVerification) error {
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, e := scanRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM cube_recovery WHERE recovery_id=?`, v.RecoveryID))
		if e != nil {
			return e
		}
		if e = validateRecoveryVerification(j, v); e != nil {
			return e
		}
		if e = recoveryFrozen(ctx, tx, j); e != nil {
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE admission_key=? AND runtime_id=? AND token=? AND state='active' AND charged=1`, "app:"+j.AppID, j.Target.RuntimeID, v.AdmissionToken).Scan(&count); e != nil {
			return e
		}
		if count != 1 {
			return cube.ErrAdmissionPending
		}
		raw, _ := json.Marshal(v)
		if j.Phase == "verified" && j.Verification == string(raw) {
			return nil
		}
		if j.Phase != "created" && j.Phase != "verified" {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_recovery SET phase='verified',verification_json=?,updated_at=? WHERE recovery_id=?`, string(raw), time.Now().Unix(), j.ID)
		return e
	})
}
func (s *Store) CommitCubeRecovery(ctx context.Context, id, expectedRuntime string, expectedRevision int64) error {
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		j, e := scanRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM cube_recovery WHERE recovery_id=?`, id))
		if e != nil {
			return e
		}
		if j.Old.RuntimeID != expectedRuntime || j.Old.ConfigRevision != expectedRevision {
			return ErrConflict
		}
		if j.Phase == "complete" {
			var current string
			e = tx.QueryRowContext(ctx, `SELECT runtime_id FROM runtime_binding WHERE sandbox_id=?`, j.SandboxID).Scan(&current)
			if e != nil {
				return e
			}
			if current != j.Target.RuntimeID {
				return ErrConflict
			}
			return nil
		}
		if j.Phase != "verified" {
			return errors.New("verified recovery journal required before binding switch")
		}
		var v CubeRecoveryVerification
		if json.Unmarshal([]byte(j.Verification), &v) != nil {
			return errors.New("invalid recovery verification")
		}
		if e = validateRecoveryVerification(j, v); e != nil {
			return e
		}
		if e = recoveryFrozen(ctx, tx, j); e != nil {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cube_admission WHERE admission_key=? AND runtime_id=? AND token=? AND state='active' AND charged=1`, "app:"+j.AppID, j.Target.RuntimeID, v.AdmissionToken).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return cube.ErrAdmissionPending
		}
		r, e := tx.ExecContext(ctx, `UPDATE runtime_binding SET runtime_id=?,template_id=?,domain=?,token_ciphertext=?,token_nonce=?,config_applied_revision=? WHERE sandbox_id=? AND runtime_id=? AND config_revision=?`, j.Target.RuntimeID, j.Target.TemplateID, j.Target.Domain, j.Target.TokenCiphertext, j.Target.TokenNonce, j.Old.ConfigRevision, j.SandboxID, j.Old.RuntimeID, j.Old.ConfigRevision)
		if e != nil {
			return e
		}
		if n, e := r.RowsAffected(); e != nil || n != 1 {
			return ErrConflict
		}
		if _, e = tx.ExecContext(ctx, `UPDATE sandbox SET status='running',error_message=NULL,stopped_at=NULL,updated_at=? WHERE id=? AND runtime_provider='cube'`, time.Now().Unix(), j.SandboxID); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE cube_recovery SET phase='complete',updated_at=? WHERE recovery_id=?`, time.Now().Unix(), j.ID)
		return e
	})
}
