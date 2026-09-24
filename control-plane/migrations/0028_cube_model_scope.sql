CREATE TABLE cube_model_scope (
 task_id TEXT PRIMARY KEY REFERENCES task(task_id) ON DELETE CASCADE,
 sandbox_id TEXT NOT NULL REFERENCES sandbox(id) ON DELETE CASCADE,
 bridge_hash BLOB NOT NULL CHECK(length(bridge_hash)=32),
 expires_at INTEGER NOT NULL
);
CREATE TRIGGER cube_model_scope_finish AFTER UPDATE OF status ON task
WHEN NEW.status != 'running'
BEGIN
 DELETE FROM cube_model_scope WHERE task_id=NEW.task_id;
END;
