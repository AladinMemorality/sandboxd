-- Retain every aborted journal and its task scope before an explicit replan.
ALTER TABLE runtime_migration ADD COLUMN archive_generation TEXT NOT NULL DEFAULT '';
CREATE TABLE runtime_migration_attempt (
 sandbox_id TEXT NOT NULL,
 archive_generation TEXT NOT NULL,
 journal_json TEXT NOT NULL,
 task_ids_json TEXT NOT NULL,
 retained_at INTEGER NOT NULL,
 PRIMARY KEY(sandbox_id,archive_generation)
);
