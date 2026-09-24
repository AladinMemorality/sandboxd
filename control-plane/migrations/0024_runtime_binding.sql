ALTER TABLE sandbox ADD COLUMN runtime_provider TEXT NOT NULL DEFAULT 'docker' CHECK(runtime_provider IN ('docker','cube'));
CREATE TABLE runtime_binding (
 sandbox_id TEXT PRIMARY KEY REFERENCES sandbox(id) ON DELETE CASCADE,
 provider TEXT NOT NULL CHECK(provider = 'cube'),
 runtime_id TEXT NOT NULL UNIQUE,
 template_id TEXT NOT NULL,
 domain TEXT NOT NULL,
 token_ciphertext BLOB NOT NULL,
 token_nonce BLOB NOT NULL
);
