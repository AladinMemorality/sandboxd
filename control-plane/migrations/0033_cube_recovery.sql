-- Offline Cube-to-Cube recovery, deliberately separate from Docker migration.
-- No cascading foreign keys: recovery identities and credentials survive row deletion.
CREATE TABLE cube_recovery (
 recovery_id TEXT PRIMARY KEY,
 sandbox_id TEXT NOT NULL,
 app_id TEXT NOT NULL,
 owner_token TEXT NOT NULL,
 phase TEXT NOT NULL CHECK(phase IN ('held','fenced','creating','created','verified','complete')),
 old_runtime_id TEXT NOT NULL,
 old_template_id TEXT NOT NULL,
 old_domain TEXT NOT NULL,
 old_token_ciphertext BLOB NOT NULL,
 old_token_nonce BLOB NOT NULL,
 config_revision INTEGER NOT NULL,
 config_applied_revision INTEGER NOT NULL,
 config_fingerprint TEXT NOT NULL,
 task_fingerprint TEXT NOT NULL,
 task_count INTEGER NOT NULL,
 source_artifacts_json TEXT NOT NULL,
 source_artifact_paths_json TEXT NOT NULL,
 artifacts_sha256 TEXT NOT NULL,
 target_template_id TEXT NOT NULL,
 target_domain TEXT NOT NULL,
 supervisor_sha256 TEXT NOT NULL,
 planned_token_ciphertext BLOB NOT NULL,
 planned_token_nonce BLOB NOT NULL,
 old_admission_json TEXT NOT NULL,
 hold_token TEXT NOT NULL,
 fence_sha256 TEXT NOT NULL DEFAULT '',
 operation_token TEXT NOT NULL DEFAULT '',
 request_sha256 TEXT NOT NULL DEFAULT '',
 new_runtime_id TEXT NOT NULL DEFAULT '',
 new_token_ciphertext BLOB,
 new_token_nonce BLOB,
 verification_json TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX cube_recovery_incomplete ON cube_recovery(sandbox_id) WHERE phase<>'complete';
CREATE UNIQUE INDEX cube_recovery_target ON cube_recovery(new_runtime_id) WHERE new_runtime_id<>'';
CREATE TABLE cube_runtime_quarantine (
 runtime_id TEXT PRIMARY KEY,
 recovery_id TEXT NOT NULL,
 sandbox_id TEXT NOT NULL,
 created_at INTEGER NOT NULL
);
