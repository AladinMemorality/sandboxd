CREATE TABLE app_runtime (
 app_id TEXT PRIMARY KEY REFERENCES app(id) ON DELETE CASCADE,
 provider TEXT NOT NULL CHECK(provider='cube')
);
INSERT OR IGNORE INTO app_runtime(app_id,provider)
 SELECT app_id,'cube' FROM sandbox WHERE runtime_provider='cube' AND app_id IS NOT NULL;
