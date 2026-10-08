-- Coding requests wait durably before consuming a worker's configured coding concurrency.
-- Per-task environment (including project bridge credentials) is encrypted.
CREATE TABLE cube_task_queue (
 task_id TEXT PRIMARY KEY REFERENCES task(task_id) ON DELETE CASCADE,
 worker_id TEXT NOT NULL,
 ciphertext BLOB NOT NULL,
 nonce BLOB NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','preparing','dispatched')),
 claim_token TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0,
 next_try INTEGER NOT NULL DEFAULT 0,
 dispatched_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX cube_task_queue_worker ON cube_task_queue(worker_id,state,next_try,task_id);
-- Keep replay metadata after deleting the encrypted dispatch payload. Requests
-- cancelled before dispatch have no guest event log and must never wake a VM.
CREATE TABLE cube_task_queue_history (
 task_id TEXT PRIMARY KEY REFERENCES task(task_id) ON DELETE CASCADE,
 dispatched_at INTEGER NOT NULL DEFAULT 0,
 terminal_only INTEGER NOT NULL DEFAULT 0 CHECK(terminal_only IN (0,1))
);
