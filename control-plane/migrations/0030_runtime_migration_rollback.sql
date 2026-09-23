-- Rollback configuration is frozen before quiescence. Changed configurations
-- require a fresh Docker container; the stopped original stays recoverable.
ALTER TABLE runtime_migration ADD COLUMN rollback_config_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_migration ADD COLUMN rollback_recreate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_migration ADD COLUMN retained_docker_name TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_migration ADD COLUMN retained_docker_retired INTEGER NOT NULL DEFAULT 0;
-- Older rollback code only admitted unchanged configurations. Preserve recovery
-- of an already acknowledged old rollback without guessing a changed config.
UPDATE runtime_migration SET rollback_config_fingerprint=config_fingerprint
 WHERE phase IN ('rollback_started','rollback_archived','rollback_restored','rolled_back');
