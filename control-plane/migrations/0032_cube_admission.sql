-- Reservations survive controller/CLI crashes and uncertain remote mutations.
CREATE TABLE cube_admission_policy (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  max_active INTEGER NOT NULL CHECK(max_active>0),
  profile TEXT NOT NULL
);
CREATE TABLE cube_admission (
  admission_key TEXT PRIMARY KEY,
  runtime_id TEXT NOT NULL DEFAULT '',
  template_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  token TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('pending','active','released','deleted')),
  charged INTEGER NOT NULL CHECK(charged IN (0,1))
);
CREATE UNIQUE INDEX cube_admission_runtime ON cube_admission(runtime_id) WHERE runtime_id<>'';
