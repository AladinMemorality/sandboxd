# Independent real platform PostgreSQL restore candidate

An independent production database snapshot was restored successfully on
26 September 2026 into the isolated fixture described below. The actual receipt
is [live-validation-2026-09-26.json](live-validation-2026-09-26.json). All five
table fingerprints and the private owner/upload relationship matched; independent
inspection confirmed fixture removal. Earlier failed attempts remain recorded.
This establishes a separate platform-data proof without starting a copied
production platform/controller or granting it live credentials. It is not the
full worker-pair restore, guest SQL proof, HTTP screenshot ACL test or S3 restore.

Read-only 26 September prerequisites: production server17.11, database36,255,411B;
host `pg_dump`/`pg_restore`17.11; cached official17-alpine amd64 image
`postgres@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24`
(117,212,703B). Those observations are not execution receipts.

The source phase opens one repeatable-read, read-only transaction and exports its
snapshot. Counts/SHA256 fingerprints and `pg_dump --snapshot` use that exact same
snapshot even while ordinary sessions update elsewhere. The query returns no
raw session hashes, account identities or content. It fingerprints complete rows
of waitlist, sessions, published apps, uploads and schema versions, plus the exact
owned private cover's noncredential provenance. Source data must fit256MiB and
the custom-format dump128MiB; larger data needs explicit resource review.

Review and pin all **five** runtime source files, preserving this exact layout
inside a private staging directory:

```text
PRIVATE-REVIEWED-SOURCE/
├── cold_pair.py
├── select_role_members.py
└── platform-pg-verify/
    ├── capture.mjs
    ├── provenance.sql
    └── verify.py
```

`verify.py` imports `select_role_members.py` from its parent directory, and that
helper imports `cold_pair.py`. Copying only the three files in
`platform-pg-verify/` is insufficient. Keep these reviewed helpers alongside the
subdirectory; do not substitute installed or unrelated versions.

Run the source phase from the staged layout:

```text
/opt/baarcha/node22/bin/node --env-file=/opt/baarcha/landing.env \
  /PRIVATE-REVIEWED-SOURCE/platform-pg-verify/capture.mjs \
  /opt/baarcha-bench/cube-platform-pg-SOURCE-NEW
```

The new directory contains intent, custom-format `platform-db`, bounded private
log and `source.json` with actual dump/query hashes and source fingerprints. A
failure retains incomplete evidence; never rename an incomplete file or invent a
source receipt. Do not disclose the connection URL, source dump or its contents.
The receipt directory is private; no production SQL writes, tasks, models,
notifications or service changes are performed.

Independently hash/review `source.json`, then run only in an approved bounded
fixture window with a **new** private stage:

```text
python3 /PRIVATE-REVIEWED-SOURCE/platform-pg-verify/verify.py \
  --source /opt/baarcha-bench/cube-platform-pg-SOURCE-NEW/source.json \
  --source-sha256 ACTUAL-REVIEWED-SOURCE-SHA \
  --stage /ROOT0700-PARENT/RESTORE-NEW --execute
```

The verifier pins the local Docker Unix API and cached immutable image, never
pulls implicitly, and creates only one uniquely labelled own container. It has
networknone, no ports, no bind mounts, no Docker socket inside it, readonly root,
all capabilities dropped, no new privileges,1CPU,512MiB memory+swap ceiling,
128PIDs and tmpfs data384MiB mounted at the exact image-declared
`/var/lib/postgresql/data` volume target, preventing an anonymous Docker volume. PostgreSQL only listens on its private Unix socket.
Readiness requires the final PostgreSQL process as container PID 1 and a
successful SQL probe within 30 seconds; the entrypoint's temporary postmaster
cannot satisfy it. Startup failures retain bounded private diagnostics.
The source dump streams through `docker exec` stdin; production credentials are
not copied into the container. Restore is bounded300s and uses one transaction
with exit-on-error into its new fixed `restoreproof` database. Source and restored
provenance must match exactly; private project/owner/upload relationships are
also checked explicitly. Only counts/hashes are recorded in the success receipt.

Before comparing fingerprints, the verifier saves bounded root-only query
stdout/stderr, restored server version/database locale metadata, and per-table
comparison flags. Phase receipts identify whether restore, query parsing or
comparison failed; no private query output is printed. These diagnostics do not
relax equality or replace the pinned provenance query. Its text-key ordering
uses explicit `COLLATE "C"` on both sides, so fingerprints do not depend on the
source and destination operating systems' locale implementations. All complete
row hashes must still match; this does not relax equality.
Failed stages remain intact; each reviewed rerun requires a new stage.

Cleanup rechecks full container ID, exact name/label/image and isolation before
stopping/removing only that fixture. Unknown/ambiguous identity is retained for
manual reconciliation. No volume pruning or forced deletion occurs. Inspect the
private stage and local Docker inventory after any failure before retrying. A
success also requires independently observed container absence. The bounded stop
may terminate only this disposable PostgreSQL fixture after30s; no source data
or live container is mounted or stopped.

This independent source snapshot cannot later be relabelled as the full paired
backup. The final pair must contain its own contemporaneous platform dump and
its corresponding provenance receipt, restored again from the independently
read-back encrypted artifact. That final capture integration remains separate.
HTTP ACL checks and independent S3 object recovery remain unproven here.
