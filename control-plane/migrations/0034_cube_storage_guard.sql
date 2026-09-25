-- Once enrolled, omission of the guard cannot disable it. Debits and CPU
-- admission are committed under the same SQLite transaction/write lock.
CREATE TABLE cube_storage_policy (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 contract TEXT NOT NULL,
 generation INTEGER NOT NULL DEFAULT 0,
 observation_json TEXT NOT NULL DEFAULT '',
 started_ns INTEGER NOT NULL DEFAULT 0,
 spent_bytes INTEGER NOT NULL DEFAULT 0 CHECK(spent_bytes>=0),
 last_clock_ns INTEGER NOT NULL DEFAULT 0,
 clock_boot_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE cube_storage_grant (
 token TEXT PRIMARY KEY,
 admission_key TEXT NOT NULL,
 granted_ns INTEGER NOT NULL,
 grant_boot_id TEXT NOT NULL,
 released_ns INTEGER,
 released_boot_id TEXT,
 bytes INTEGER NOT NULL CHECK(bytes=12884901888)
);
CREATE INDEX cube_storage_grant_carry ON cube_storage_grant(released_ns);
