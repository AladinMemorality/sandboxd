-- Runtime is measured from durable controller state transitions, not creation
-- time or guest CPU time. Existing histories cannot be reconstructed.
CREATE TABLE sandbox_runtime_accounting (
  sandbox_id TEXT PRIMARY KEY REFERENCES sandbox(id) ON DELETE CASCADE,
  tracking_since INTEGER NOT NULL,
  running_since INTEGER,
  start_known INTEGER NOT NULL DEFAULT 0,
  total_seconds INTEGER NOT NULL DEFAULT 0 CHECK(total_seconds >= 0)
);
INSERT INTO sandbox_runtime_accounting(sandbox_id, tracking_since, running_since)
SELECT id, unixepoch(), CASE WHEN status='running' THEN unixepoch() END FROM sandbox;
CREATE TRIGGER sandbox_runtime_insert AFTER INSERT ON sandbox BEGIN
  INSERT INTO sandbox_runtime_accounting(sandbox_id, tracking_since, running_since, start_known)
  VALUES(NEW.id, unixepoch(), CASE WHEN NEW.status='running' THEN unixepoch() END, 1);
END;
CREATE TRIGGER sandbox_runtime_transition AFTER UPDATE OF status ON sandbox
WHEN OLD.status <> NEW.status BEGIN
  UPDATE sandbox_runtime_accounting SET
    total_seconds = total_seconds + CASE WHEN running_since IS NOT NULL THEN max(0,unixepoch()-running_since) ELSE 0 END,
    running_since = CASE WHEN NEW.status='running' THEN unixepoch() END,
    start_known = 1
  WHERE sandbox_id=NEW.id;
END;
-- Recovery can replace the VM while leaving the sandbox marked running.
CREATE TRIGGER sandbox_runtime_rebind AFTER UPDATE OF runtime_id ON runtime_binding
WHEN OLD.runtime_id <> NEW.runtime_id BEGIN
  UPDATE sandbox_runtime_accounting SET
    total_seconds = total_seconds + CASE WHEN running_since IS NOT NULL THEN max(0,unixepoch()-running_since) ELSE 0 END,
    running_since = CASE WHEN running_since IS NOT NULL THEN unixepoch() END,
    start_known = 0
  WHERE sandbox_id=NEW.sandbox_id;
END;
