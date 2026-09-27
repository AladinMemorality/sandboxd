-- Operator-managed moves retain the stopped source until target verification.
CREATE TABLE cube_relocation (
 id TEXT PRIMARY KEY,
 sandbox_id TEXT NOT NULL,
 phase TEXT NOT NULL CHECK(phase IN ('fenced','complete','aborted')),
 baseline_json TEXT NOT NULL,
 target_runtime_id TEXT NOT NULL DEFAULT '',
 verification_sha256 TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE UNIQUE INDEX cube_relocation_open ON cube_relocation(sandbox_id) WHERE phase='fenced';
