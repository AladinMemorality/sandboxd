ALTER TABLE runtime_binding ADD COLUMN config_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_binding ADD COLUMN config_applied_revision INTEGER NOT NULL DEFAULT 0;
UPDATE runtime_binding SET config_revision=1 WHERE sandbox_id IN (
 SELECT s.id FROM sandbox s JOIN app_config c ON c.app_id=s.app_id
 WHERE c.access_policy IN ('runtime_access','both')
);
CREATE TRIGGER cube_config_insert AFTER INSERT ON app_config
WHEN NEW.access_policy IN ('runtime_access','both')
BEGIN
 UPDATE runtime_binding SET config_revision=config_revision+1
 WHERE sandbox_id IN (SELECT id FROM sandbox WHERE app_id=NEW.app_id);
END;
CREATE TRIGGER cube_config_update AFTER UPDATE ON app_config
WHEN NEW.access_policy IN ('runtime_access','both') OR OLD.access_policy IN ('runtime_access','both')
BEGIN
 UPDATE runtime_binding SET config_revision=config_revision+1
 WHERE sandbox_id IN (SELECT id FROM sandbox WHERE app_id=NEW.app_id);
END;
CREATE TRIGGER cube_config_delete AFTER DELETE ON app_config
WHEN OLD.access_policy IN ('runtime_access','both')
BEGIN
 UPDATE runtime_binding SET config_revision=config_revision+1
 WHERE sandbox_id IN (SELECT id FROM sandbox WHERE app_id=OLD.app_id);
END;
