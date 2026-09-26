# Embedded PostgreSQL source preparation

`postgres_cohort.py` is a dedicated adapter for the existing MyHomeTroc project
and its bundled PostgreSQL 18. It permits only that exact project's known live
Unix socket to defer the **online** home check. The ordinary frozen manifest,
archive, transfer and target verification paths remain strict and unchanged.

The source must stop through the existing task-aware controller path. Before
any export, `postgres_source.py` requires the current postmaster's clean-shutdown
log acknowledgment and disappearance of its PID, socket and socket lock. It
binds the stopped source's log and control-file hashes for the frozen inventory.
A stopped container alone is insufficient evidence of PostgreSQL shutdown.

The dedicated production migration passed on September26: clean source stop,
strict full transfer, target SQL health and application table reads, normal API
acceptance and independently verified restoration. See
[the production evidence](results/2026-09-26-embedded-postgres/README.md).
The original Docker source remains stopped and retained. No PostgreSQL data or
sockets were excluded from an export manifest. Any future operation requires a
fresh plan; the successful production plan is consumed.

Fourteen focused tests passed locally and on Linux. The complete 54-test cutover
suite passed in an isolated Linux review directory with its own loopback Caddy
fixtures. Local full-suite execution lacked Caddy; a first remote invocation
inherited the private staging umask and failed an existing 0755 cache fixture.
Rerunning the tests with their normal 0022 umask passed; production metadata
validation was not changed.
