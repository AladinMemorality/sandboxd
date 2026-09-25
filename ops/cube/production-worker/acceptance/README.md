# Fresh-worker workload acceptance

These opt-in fixtures create only disposable synthetic projects on
`baarcha-cube-worker-01`. They require the fresh private credential file
`/root/cube-production/test-secrets.json`; never copy benchmark credentials.
Serialize fixture families and verify deletion before handing the worker to the
next family. They must not run alongside customer workloads or template builds.

Build `main.go`, `presets.go` and `home.go` in a temporary control-plane command
directory, separately from the API test files. Run the resulting executable as
`cube-worker-acceptance preset TEMPLATE PRESET REPORT`. It verifies the actual
two-CPU/two-GiB allocation, expected frontend/backend/worker behavior, unchanged
process identity after pause/resume, and strict quiesced app/home exports. Its
timings are single samples, not percentiles or production traffic benchmarks.
Seven presets use this fixture; PostgreSQL has the separate lifecycle test.

Copy `postgres_acceptance_test.go` and `reload_acceptance_test.go` into a temporary
copy of `control-plane/internal/api`, then build that package's test binary. Run
from its API directory with all migrations and normal repository fixtures present.
Use `CUBE_POSTGRES_FUNCTIONAL=1`, `CUBE_POSTGRES_TEMPLATE=<fresh node-postgres ID>`
and `CUBE_POSTGRES_STAGE=<private report directory>` for
`TestOperatorCubePostgresLifecycle`. Use `CUBE_RELOAD_FUNCTIONAL=1`,
`CUBE_RELOAD_TEMPLATE=<fresh react-pro ID>` and `CUBE_RELOAD_STAGE=<private report
directory containing disposable-reload marker>` for `TestOperatorCubeViteReload`.
Place the exact `scripts/vite-reload-regression.mjs` in the reload stage as
`reload-regression.mjs`; the guest command explicitly selects the reviewed
installed Vite patch. Both API fixtures must be compiled together because they
share a cleanup helper. Cleanup requires controller deletion and an independent
Cube GET404 for the captured immutable VM ID, under a fresh bounded deadline.
`TestOperatorAcceptanceCleanupVerifiesRemoteDeletion` exercises that sequence
using a local HTTP fixture without creating guests.

Both fixtures use the real SQLite-backed admission guard and global provider
selection. The PostgreSQL fixture checks writes, publication/remix separation,
configuration/reload, pause/resume and restore using synthetic data. It does not
replace MyHomeTroc's actual stopped-source migration and SQL verification. Its
private-home roundtrip imports into the same quiesced guest; it does not claim
new-machine backup restoration.

Generic network access is denied in these fixtures. Real registry clients,
model/bridge calls, private capture, customer native dependencies, full-worker
crash recovery and production HTTPS routes require their own evidence.

The staged `run-api.sh` wrapper is invoked explicitly by the coordinator after
exclusive worker handoff. It pins the two reviewed template IDs, verifies the
compiled binary hash, uses private `/data/acceptance-temp` for SQLite test files,
and refuses an existing run ID. Every live fixture fsyncs an `owned-guests.ndjson`
journal before subsequent lifecycle mutations; retain this for cleanup review if
a process exits before writing its final report. Missing final cleanup proof
causes the wrapper to fail. A successful compile or skipped opt-in test is never
recorded as live acceptance.

Before each staged run, the coordinator must create root-owned mode0600
`handoff.json` in the stage, with this shape and a Unix expiry no more than
30 minutes ahead:

```json
{"purpose":"DISPOSABLE_CUBE_API_HANDOFF","family":"postgres","run_id":"postgres-01","no_customer_guests":true,"previous_family_cleanup_verified":true,"expires_at":0}
```

Replace the expiry and use `family:"reload"` for the reload test. A zero expiry
is intentionally invalid. The wrapper also requires the coordinator's existing
`/root/cube-production/security-fixture-complete`, then checks authenticated
HTTP inventory and independent CLI all-state inventory are both empty. It
consumes the handoff once under `/run/lock/cube-operator-acceptance.lock` before
launching. Other operator fixture families should honor the same lock; explicit
serialized handoff remains required. The worker hostname alone is never enough.
