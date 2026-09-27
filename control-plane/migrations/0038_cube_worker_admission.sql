-- Preserve the original VPS contract while adding independently charged workers.
ALTER TABLE cube_admission ADD COLUMN worker_id TEXT NOT NULL DEFAULT 'vps';
CREATE INDEX cube_admission_worker ON cube_admission(worker_id,charged,state);
CREATE TABLE cube_worker_identity (
 worker_id TEXT PRIMARY KEY,
 node_id TEXT NOT NULL UNIQUE
);
ALTER TABLE cube_storage_grant ADD COLUMN worker_id TEXT NOT NULL DEFAULT 'vps';
CREATE INDEX cube_storage_grant_worker ON cube_storage_grant(worker_id,released_ns);

ALTER TABLE cube_admission_policy RENAME TO cube_admission_policy_legacy;
CREATE TABLE cube_admission_policy (
 singleton INTEGER NOT NULL DEFAULT 1 CHECK(singleton=1),
 worker_id TEXT NOT NULL DEFAULT 'vps',
 max_active INTEGER NOT NULL CHECK(max_active>0),
 profile TEXT NOT NULL,
 PRIMARY KEY(singleton,worker_id)
);
INSERT INTO cube_admission_policy(singleton,max_active,profile)
 SELECT singleton,max_active,profile FROM cube_admission_policy_legacy;
DROP TABLE cube_admission_policy_legacy;

ALTER TABLE cube_storage_policy RENAME TO cube_storage_policy_legacy;
CREATE TABLE cube_storage_policy (
 singleton INTEGER NOT NULL DEFAULT 1 CHECK(singleton=1),
 worker_id TEXT NOT NULL DEFAULT 'vps',
 contract TEXT NOT NULL,
 generation INTEGER NOT NULL DEFAULT 0,
 observation_json TEXT NOT NULL DEFAULT '',
 started_ns INTEGER NOT NULL DEFAULT 0,
 spent_bytes INTEGER NOT NULL DEFAULT 0 CHECK(spent_bytes>=0),
 last_clock_ns INTEGER NOT NULL DEFAULT 0,
 clock_boot_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(singleton,worker_id)
);
INSERT INTO cube_storage_policy(singleton,contract,generation,observation_json,started_ns,spent_bytes,last_clock_ns,clock_boot_id)
 SELECT singleton,contract,generation,observation_json,started_ns,spent_bytes,last_clock_ns,clock_boot_id FROM cube_storage_policy_legacy;
DROP TABLE cube_storage_policy_legacy;
