-- Reviewed owner-home scope is immutable once a migration is planned.
ALTER TABLE runtime_migration ADD COLUMN home_manifest_json TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_migration ADD COLUMN home_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_migration ADD COLUMN rollback_home_sha256 TEXT NOT NULL DEFAULT '';
