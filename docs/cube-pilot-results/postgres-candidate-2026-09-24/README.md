# Optional PostgreSQL candidate acceptance — 2026-09-24

The actual isolated Cube candidate passed the synthetic PostgreSQL lifecycle fixture in **32.92 seconds**. This is functional acceptance, not a throughput benchmark or production/network-isolation approval. No production project, database, runtime mapping or admission flag changed.

The candidate uses PostgreSQL **18.4** on a private Unix socket, with its full cluster in the owner's private home outside published source. PostgreSQL starts only for the explicitly selected `node-postgres` preset or a deliberately added manifest worker. Existing seven starter manifests do not start or initialize it.

| Artifact | Value |
| --- | --- |
| Shared composed image | `sha256:3fd511ce3b190865f49ea3875c8743daffd7c80930d892a073b9b3e03b06aa0f` |
| Prepared PostgreSQL image | `sha256:b72798aaa51aa770dc9c950eefb1f0ea5fd17fc9454e6b94c1b8bf4be7f40e85` |
| Template | `tpl-2aa3ca52b0d84b3c8e49085d` |
| Resources | 1 CPU, 1,024 MiB, 10 GiB writable layer |
| Source | Runtime `da5e98d` plus explicit manifest activation host API and this fixture |

[The fixture](../../../ops/cube/functional/2026-09-24/cube_postgres_acceptance_test.go) uses real Cube VMs and the actual owner API. It verifies:

- Committed SQL write/read and actual SQL `/health` readiness.
- Private database data excluded from published source; a source remix gets a distinct VM/credentials and an empty database.
- Data survives pause/resume and supervisor config reexec.
- An explicit manifest reload starts a newly added worker in the same VM and preserves the SQL record. Repeating the unchanged manifest preserves the supervisor boot time, web PID and configuration revision.
- Owner source restore applies frozen source while preserving the app, sandbox, VM, transport credentials and private database.
- Quiesced full home-v2 export/import preserves the PostgreSQL cluster and its committed record after resume. The archive is 4,970,993 bytes with a verified digest.
- Both owned fixture VMs are deleted during cleanup.

The [report](postgres-lifecycle-report.json) and [test output](test-output.txt) retain the observed timings. These are a single acceptance run with prepared dependencies, not warmed multi-sample performance measurements.

The first run passed through owner restore and full home export/import, then checked the notes endpoint too soon after resume and received HTTP 502. Its [report](postgres-lifecycle-report-initial.json) and [output](test-output-initial.txt) are retained. The final fixture waits for the application's real SQL health response, because the supervisor can briefly retain a previous preview-health observation while the application restarts. No runtime or database policy was weakened.

Separate real-binary worker tests exercise committed-WAL crash recovery, a cold full-cluster copy with the original removed, fresh-cluster isolation, private directory ownership and incompatible-major refusal. The composed Linux candidate ran those tests as the sandbox user under tini, with network disabled. Physical backup covers the complete cluster, including schema and WAL; a generic logical `pg_dump` export is not supplied by this embedded binary bundle.

Limits: this is synthetic data on one isolated nested host; it does not prove off-host disaster recovery, production capacity, arbitrary external database clients, or network isolation. Existing projects opt in deliberately; existing MyHomeTroc data is not relocated. Docker rollback for a PostgreSQL-enabled project requires an image containing `/opt/services/postgres`. The candidate's baked README predates the final manifest-activation paragraph in [the current recipe](../../project-postgres.md); the executable worker and supervisor code are identical, and the activation API is host-side.
