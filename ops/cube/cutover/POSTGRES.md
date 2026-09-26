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

This is preparation only: **not deployed or production accepted**. Before use,
add and verify the application's SQL-backed `/api/health` acceptance, prepare a
fresh dedicated pinned plan, and complete source and target database acceptance.
No PostgreSQL data or sockets have been excluded from an export manifest. No
source container was stopped while developing these helpers.

Fourteen focused tests passed locally and on Linux. The complete 54-test cutover
suite passed in an isolated Linux review directory with its own loopback Caddy
fixtures. Local full-suite execution lacked Caddy; a first remote invocation
inherited the private staging umask and failed an existing 0755 cache fixture.
Rerunning the tests with their normal 0022 umask passed; production metadata
validation was not changed.
