# MyHomeTroc PostgreSQL migration — September 26, 2026

The existing MyHomeTroc app and its embedded PostgreSQL 18 now execute in Cube.
The original Docker source is retained stopped; the complete owner home and
workspace, including the database, were transferred through the ordinary native
archive/hash verification. No database conversion or manifest exclusion was used.

Source health executed SQL successfully before migration. Under the task and
traffic fence, PostgreSQL acknowledged clean shutdown and removed its socket,
socket lock and PID file. The dedicated adapter pinned the stopped log and
control-file hashes before strict frozen inventory/export.

The target passed normal HTML/JavaScript, three source-file checks, historical
task checks, and the application's SQL-backed health and public catalog APIs.
The catalog read items/settings/wa_state successfully and returned five published
items. This did not send any WhatsApp message or create a customer session.

Unit `cube-postgres-migrate-20260926-01.service`, invocation
`8d8e99a1cc194c66be216f5826fdd570`, exited0. Production reopened at
**22:01:43 UTC**, after294.623seconds. Independent restoration reacquired all
four locks, verified full online routes/controller readiness and retained source.
Fleet:55Cube bindings (54customer projects plus fixture),18Docker;54complete
migration journals, no active coding tasks. Default/controller retirement pending.

Private journal:
`/opt/baarcha-cube/worker-01/maintenance/postgres-migrate-20260926-01`.
Stage: `/opt/baarcha-bench/cube-cohort-postgres-20260926-01`.
Plan SHA256:`5b1455f19bf21e32b79146b6260db4e673925befec89f3d35f9739fb4ebe09b6`.
The stage and plan are consumed. Do not rerun them.
