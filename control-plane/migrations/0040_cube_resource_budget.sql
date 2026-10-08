-- Immutable per-worker resource contract. Existing uniform workers are unchanged.
CREATE TABLE cube_resource_budget (
    worker_id TEXT PRIMARY KEY,
    contract TEXT NOT NULL
);

-- Preserve all outstanding and released grants while permitting reviewed
-- per-template sizes. The largest supported contract is 32GiB disk + 8GiB RAM.
ALTER TABLE cube_storage_grant RENAME TO cube_storage_grant_legacy;
CREATE TABLE cube_storage_grant (
    token TEXT PRIMARY KEY,
    admission_key TEXT NOT NULL,
    granted_ns INTEGER NOT NULL,
    grant_boot_id TEXT NOT NULL,
    released_ns INTEGER,
    released_boot_id TEXT,
    bytes INTEGER NOT NULL CHECK(bytes>0 AND bytes<=42949672960 AND bytes%1048576=0),
    worker_id TEXT NOT NULL DEFAULT 'vps'
);
INSERT INTO cube_storage_grant
    (token,admission_key,granted_ns,grant_boot_id,released_ns,released_boot_id,bytes,worker_id)
SELECT token,admission_key,granted_ns,grant_boot_id,released_ns,released_boot_id,bytes,worker_id
FROM cube_storage_grant_legacy;
DROP TABLE cube_storage_grant_legacy;
CREATE INDEX cube_storage_grant_carry ON cube_storage_grant(released_ns);
CREATE INDEX cube_storage_grant_worker ON cube_storage_grant(worker_id,released_ns);
