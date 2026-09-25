# Minimal Motion worker UDS release — prepared, not installed

Read-only inspection on September 25 found the live dedicated worker's 20
selected package/proxy/server/render/shared files exactly match handed-over
feature revision `843e9f9`. This includes directing, voice/presenter, narration,
soundtrack and rendering code. The comparison is recorded in
`source-comparison.json`; it does not infer ownership from a project name or
replace unreviewed live changes.

The coordinated candidate is platform PR9 commit
`08244741f2c595f433fffacdf1ec6f3cd5a8a2ff`. Its only worker-runtime delta is:

1. Replace `server/index.mjs` with the reviewed version importing the two helpers.
2. Add `server/listen.mjs` for the authenticated Unix socket alongside the explicit
   existing TCP listener, sharing one queue and request handler.
3. Add `server/multipart.mjs` for complete-form and 50 MiB validation before writes.
4. Add this directory's `20-cube-uds.conf` as a service drop-in.

`manifest.json` pins old/new hashes. No dependency changed: the lockfile is
identical and the package.json difference is only a test command. Do not run
`install-worker.sh`, `install-production.py`, npm install, a frontend build,
rendering or the customer frontend installer for this narrow update. The latter
installers have broader ownership, dependency and route effects than needed.

The local prepared tar `/private/tmp/cube-motion-review-20260925/worker-uds-source.tar`
contains only the three regular source files, with relative paths, no links,
zero timestamps and 0644 modes. SHA-256:
`432309a16306180f972b1f50bb62c0dc2c2aaaf84d4a9e3501763ad785e3cb7a`.
It has not been copied to or installed on the VPS by this review.

## Exact staged actions for root review

Use the existing platform/runtime deployment locks and operator maintenance
coordination. Pin the actual current controller image/ID and platform revision
again; the profile's prior controller pin is already obsolete. Hold the reviewed
writer fence for Motion API/proxy traffic while backing up and restarting its
worker. A point-in-time queue count is **not** a fence: before service stop,
require no `queued` or `running` jobs and drain in-flight uploads/API requests.
The inspected snapshot contained seven projects, twenty ready jobs and six failed
jobs, with no active jobs; do not assume that still holds at execution time.

1. In a new root-private stage, verify candidate hashes, the entire selected live
   source comparison, the existing unit hash, canonical paths, regular single-link
   source files, current inode/owner/mode, and absent new helper/drop-in paths.
   Refuse drift or symlinks. Preserve the current service environment privately;
   do not print credentials. Assert `STUDIO_WORKER_KEY` is nonempty,
   `STUDIO_WORKER_URL` is empty, and the existing explicit TCP bind/port are
   unchanged (`172.19.0.1:8332` at inspection).
2. After fencing and draining, stop **only** `baarcha-motion-worker.service`.
   Independently confirm no worker process/children remain before data escrow.
   Preserve the exact original three-file existence/mode/owner state, unit and
   all drop-ins, private worker.env, and a consistent closed backup of
   `/opt/baarcha/motion-studio/data` and `/opt/baarcha/motion-studio/home`.
   Keep numeric ownership, modes and link semantics; do not dereference links or
   silently omit special metadata. Hash/fsync the backup and record a private
   file inventory. No data-file rewrites or recursive chown are part of release.
3. Install only the three verified regular source files using same-directory
   temporary files, fsync, preserved service ownership and 0644 mode, atomic
   rename and parent-directory fsync. Install the additive drop-in at
   `/etc/systemd/system/baarcha-motion-worker.service.d/20-cube-uds.conf` with
   root ownership/0644, refusing an existing unreviewed file. The worker remains
   stopped across this multi-file operation. Existing package, lockfile, proxy,
   renderer, shared code, worker.env, customer frontend and data remain unchanged.
4. `systemctl daemon-reload`, then start that exact worker. Verify service PID,
   service UID, retained resource limits (6 GiB / 250% CPU), socket directory
   service-owned0750 and socket0660, and both Unix/TCP listeners. Check `/api/health`
   on both transports and authenticated read-only `/api/projects` through each;
   compare the pre-stop project/asset/export/job identities privately. A health
   response alone does not prove worker authorization or retained projects.
   Verify missing/wrong bearer receives401 for `/api/projects`.
5. If startup/read-only validation fails before reopening traffic, stop the
   changed worker and atomically restore only its prior source/drop-in state;
   reload/start and verify the old TCP path. Preserve all failed evidence. Do
   **not** rewind data or silently replay work: investigate any changed jobs or
   writes. Reopen the reviewed writer fence only after known health/identity.

Do not expose a new TCP bind or modify nftables/UFW. The existing TCP listener and
access refresh timer remain needed by the still-Docker Motion application. The
drop-in adds one Unix listener; both transports have the same bearer checks and
one shared queue. A restart is observable downtime and must occur in the reviewed
maintenance window rather than being described as a zero-impact file update.

## Remaining Cube prerequisites

`template-observation.json` records the actual read-only listing: eight known
READY templates plus one retained FAILED template. All eight READY templates
predate the capability; none is relabeled as supporting `motion-worker-v1`.
The existing react-vite ID is `tpl-5abec4cb4fcc41cc8e611f69`. Keep it and the
failed record intact.

Follow the existing parent README's new-template derivation, using the reviewed
credential-free immutable base, current reviewed runtimed source, a new alias,
2 CPUs / 2 GiB / 10 GiB and direct-NIC deny-all. Reuse the exact CLI options in
`ops/cube/production-worker/register-templates.py`; do not rerun its eight-template
loop. Root must coordinate the template build with the existing owned canary and
capacity accounting, persist create/job state before watching, and verify the
new READY template plus actual authenticated `motion-worker-v1` capability.

The controller additionally needs the exact persisted Motion app selection and
only `/run/baarcha-motion-studio` mounted read-only at the same path with
`bind.create_host_path=false`. The current synthetic canary's one-app allowlist
must not be mistaken for Motion authorization. Any owned acceptance app selection
must be explicit and temporary; the adapter must not gain a wildcard mapping.
Root reviews the actual controller recreation/configuration and scoped owned-film
acceptance before journaled migration of the real Motion application. Preserve
the encrypted original TCP worker URL/key for rollback; the new loopback named
URL is only an in-memory overlay after capability validation.

No live UDS/config/mount/template operation or customer project mutation was
performed by this preparation. Local channel/unit tests and the new controller
binary do not substitute for actual owned target upload/range/revocation proof.
