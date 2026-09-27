-- Separate from VM snapshots: immutable source revisions and fenced deployment jobs.
-- No existing project is enrolled and no production capacity is increased here.
CREATE TABLE project_revision (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL REFERENCES app(id),
    source_sha256 TEXT NOT NULL CHECK(length(source_sha256)=64),
    manifest_json TEXT NOT NULL,
    object_key TEXT NOT NULL UNIQUE,
    size_bytes INTEGER NOT NULL CHECK(size_bytes > 0),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(app_id, id)
);
CREATE TABLE project_deployment_head (
    app_id TEXT PRIMARY KEY REFERENCES app(id),
    revision_id TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK(generation > 0),
    FOREIGN KEY(app_id, revision_id) REFERENCES project_revision(app_id, id)
);
CREATE TABLE project_worker (
    id TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
    draining INTEGER NOT NULL DEFAULT 0 CHECK(draining IN (0,1)),
    priority INTEGER NOT NULL DEFAULT 100,
    cpu_millis INTEGER NOT NULL CHECK(cpu_millis > 0),
    memory_mb INTEGER NOT NULL CHECK(memory_mb > 0),
    max_active INTEGER NOT NULL CHECK(max_active > 0),
    max_starting INTEGER NOT NULL CHECK(max_starting > 0),
    min_disk_mb INTEGER NOT NULL CHECK(min_disk_mb >= 0),
    runtime_image TEXT NOT NULL,
    observed_at INTEGER NOT NULL DEFAULT 0,
    boot_id TEXT NOT NULL DEFAULT '',
    disk_available_mb INTEGER NOT NULL DEFAULT 0 CHECK(disk_available_mb >= 0),
    memory_available_mb INTEGER NOT NULL DEFAULT 0 CHECK(memory_available_mb >= 0),
    healthy INTEGER NOT NULL DEFAULT 0 CHECK(healthy IN (0,1))
);
CREATE TABLE project_deployment (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL REFERENCES app(id),
    revision_id TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK(generation > 0),
    worker_id TEXT NOT NULL REFERENCES project_worker(id),
    worker_boot_id TEXT NOT NULL,
    cpu_millis INTEGER NOT NULL CHECK(cpu_millis > 0),
    memory_mb INTEGER NOT NULL CHECK(memory_mb > 0),
    disk_mb INTEGER NOT NULL CHECK(disk_mb > 0),
    state TEXT NOT NULL CHECK(state IN ('reserved','preparing','ready','active','uncertain','released')),
    runtime_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
    FOREIGN KEY(app_id, revision_id) REFERENCES project_revision(app_id, id)
);
-- A stale heartbeat never releases a charge. Released requires provider evidence.
CREATE UNIQUE INDEX project_one_charged_deployment ON project_deployment(app_id) WHERE state <> 'released';
CREATE UNIQUE INDEX project_unique_runtime ON project_deployment(runtime_id) WHERE runtime_id <> '';
CREATE INDEX project_worker_charges ON project_deployment(worker_id, state);
