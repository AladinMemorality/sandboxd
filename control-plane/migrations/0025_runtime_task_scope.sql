-- Task results outlive sandbox deletion; retain the authenticated tenant boundary.
CREATE TABLE runtime_task_scope (
 sandbox_id TEXT PRIMARY KEY,
 owner_token TEXT NOT NULL,
 provider TEXT NOT NULL CHECK(provider='cube')
);
INSERT INTO runtime_task_scope(sandbox_id,owner_token,provider)
 SELECT s.id,a.owner_token,'cube' FROM sandbox s JOIN app a ON a.id=s.app_id
 WHERE s.runtime_provider='cube';
