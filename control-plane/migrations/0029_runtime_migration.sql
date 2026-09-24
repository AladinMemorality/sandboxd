-- Offline owner-preserving migration journal. No foreign key cascade: deletion
-- must never silently discard recovery metadata or retained source identities.
CREATE TABLE runtime_migration (
 sandbox_id TEXT PRIMARY KEY,
 phase TEXT NOT NULL,
 source_json TEXT NOT NULL,
 source_preset TEXT NOT NULL,
 config_fingerprint TEXT NOT NULL,
 target_preset TEXT NOT NULL,
 runtime_id TEXT NOT NULL DEFAULT '',
 template_id TEXT NOT NULL,
 domain TEXT NOT NULL,
 token_ciphertext BLOB,
 token_nonce BLOB,
 archive_sha256 TEXT NOT NULL DEFAULT '',
 rollback_sha256 TEXT NOT NULL DEFAULT '',
 history_sha256 TEXT NOT NULL DEFAULT '',
 rollback_history_sha256 TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL
);
CREATE TABLE runtime_migration_task (
 sandbox_id TEXT NOT NULL REFERENCES runtime_migration(sandbox_id),
 task_id TEXT NOT NULL,
 PRIMARY KEY(sandbox_id,task_id)
);
