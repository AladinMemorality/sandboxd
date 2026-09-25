# Offline Cube replacement journal

This is an operator-only Go helper, not an automatic recovery policy or a tenant
endpoint. It does not enable Cube or migrate any production project. Migration
0033 separates Cube-to-Cube replacement from the earlier Docker migration journal.
An owned live replacement using this journal passed on 2026-09-25: the retained
current-crash-disk app/home/SQL state was imported into one real replacement,
verified, and atomically rebound in an isolated controller database. See
[acceptance scope and evidence](acceptance/README.md#actual-result). This was a
synthetic controller/key and task-history fixture, not a production cutover,
platform router test, or native same-provider-ID recovery pass.

A native-host root process opens `internal/recovery.Open` with the existing
controller database, encryption key, and reviewed Cube admission profile. Opening
requires the daemon's previously created maintenance marker and exclusive flock,
and checks `/proc` for other database users. The caller must first stop controller
admission and disable older daemon auto-restarts. A container's limited PID
namespace is insufficient evidence about host writers. Opening the helper does
not stop services or drain a worker. The daemon refuses startup while any Cube
recovery journal is unfinished; read-only/offline recovery remains available.

The sequence is:

1. Independently preserve the controller's consistent SQLite database **and
   encryption key**, current runtime config, owner/control/task history, source
   native disks/archives and necessary library snapshots. A native backup plus an
   old Docker workspace is not a backup of later Cube writes. Store references to
   each retained artifact and its SHA256 in the private plan. `native_backup` and
   `controller_backup` identify retained backup artifacts or their manifests; `workspace` and `home`
   identify canonical imported archives. Canonical task history is mandatory if
   any task exists; an empty task set has its own exact digest. Keep manifests,
   paths, receipts, and encrypted credentials private.
2. `Session.Begin` checks exact source runtime ID, config revision, current app
   binding, no running tasks, and canonical task/config fingerprints. It seals a
   fresh supervisor credential and journals the old encrypted binding/admission
   record. It permanently quarantines the old runtime and retains its charged
   slot. There is no extra live slot when replacing an already charged dead app.
3. `Fence` records reviewed evidence that the old execution cannot resume and all
   old provider operations have terminated. **These are trusted operator
   assertions bound to the artifact set and source ID.** SQLite cannot prove a
   remote shim stopped. Elapsed time alone is not evidence. Without these proofs,
   replacement creation is refused.
4. `Create` durably saves a unique operation token, complete request hash, and
   pending allocation **before** one provider POST. Same-app retries cannot create
   another guest. The global durable create fence remains in force. Caller
   cancellation after reservation finishes the bounded acknowledgment; process or
   infrastructure ambiguity stays pending without expiry. `Adopt` performs only
   GET and accepts the exact running template, resources, IDs and operation token.
   Generic admission adoption cannot bypass this journal. A crash after provider
   acknowledgment but before credential storage is recoverable by exact adoption.
5. Keep the exclusive session open. `TargetClient` returns authenticated access to
   the replacement through the operator-configured private proxy; credentials are
   never printed. Import and re-export workspace, reviewed home, and canonical
   task history, apply the frozen config, then establish authenticated application
   readiness. `ResumeTarget` only touches the recorded replacement. The helper
   does not itself implement this import/re-export verifier or fabricate receipts.
6. Obtain the current target `AdmissionToken`, and submit `Verify` with exact
   source/target IDs, artifact/config/task/credential digests, and independent
   evidence digest. Every proof flag is required. `Commit` atomically compares
   source generation, config, task state, owner and current target lease, then
   swaps encrypted runtime binding and completes the journal. Stable sandbox/app
   IDs and their URLs remain unchanged. The old binding and permanent quarantine
   remain in the recovery record. Any subsequent target pause/connect invalidates
   the old verification lease and requires renewed verification.

Artifact path/hash declarations and import receipts are trusted inputs from the
operator verifier, not independent validation of arbitrary files by the store.
The generic helper cannot establish filesystem/SQL consistency, disk durability,
RPO, recovered agent sessions, or completeness of task files by hashing DB task
rows. Those require the separate real native recovery/import evidence. Never
submit successful proof flags on the strength of journal unit tests.

Interrupted plans retain their slot, credential, and artifact references. There
is intentionally no automatic reset, TTL, unquarantine, fallback to stale disks,
or removal of the retained source. If the replacement is itself lost, stop and
review that generation; this helper currently refuses a second replacement within
the same unfinished journal. This fails closed and requires a future explicit
operator transition rather than silently losing a pending allocation.
