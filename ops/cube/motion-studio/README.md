# Motion Studio: fixed worker adapter

This is an opt-in application capability, not an exception to public egress or
Cube's direct-NIC deny-all policy. Code preparation and local fixtures do not
prove that the currently deployed worker/controller/template supports it. No live
socket, mount, service, project configuration or network rule is changed here.

## Deployment contract

The controller option `SANDBOXD_CUBE_MOTION_STUDIO_APP_ID` selects exactly one
persisted application ULID. Empty means disabled. Reverse egress retains all its
existing startup/acceptance gates. A single shared handler admits at most four
concurrent HTTP requests, with no unbounded queue (`503`, `Retry-After: 1`). Each
request carries the authenticated channel's sandbox ID and runtime generation.
The controller checks the persisted Cube binding, exact app, current sandbox and
running state on entry and rechecks during long requests. Channel replacement
cancels its requests; a failed periodic authorization check cancels within the
one-second polling interval plus a two-second bounded store lookup.

Only `/run/baarcha-motion-studio/worker.sock` is dialled, using a Unix transport
without HTTP proxy discovery, DNS, redirects or TCP fallback. A symlink or
non-socket at that final path is rejected. The directory and its parents are
operator-managed; never mount a tenant-controlled path. The dedicated worker
creates the socket mode `0660` in its service-owned `0750` RuntimeDirectory. Mount
**only that directory read-only** into the controller at the identical path,
with `bind.create_host_path=false`. Current controller root with host user
namespace can connect; any later UID/user-namespace change needs explicit DAC
verification. No worker data, environment, general `/run`, Docker socket or other
host directory is mounted.

The coordinated Motion Studio worker change (separate platform branch, commit
`967ca15`) adds an optional Unix listener sharing its existing application,
queue, and bearer authorization. Keep its existing TCP listener explicit during
Docker-to-Cube transition; do not remove access for the running Docker app.

The reviewed new guest supervisor exposes the named service at
`http://127.0.0.1:3032/__cube/motion`. The target applies that URL only in an in-memory environment overlay after its
authenticated supervisor advertises `motion-worker-v1`. The encrypted original
`STUDIO_WORKER_URL` stays unchanged for Docker rollback. Its existing dedicated
`STUDIO_WORKER_KEY`, stable app/sandbox IDs and `APP_ORIGIN` are preserved. Provider
credentials remain on the dedicated worker. The installed older guest templates
must be updated before this namespace can be used. Generic `HTTPServices`,
protected host addresses, public CONNECT and native guest networking are unchanged.

## Exact HTTP contract

Paths cover Motion Studio through the handed-over `843e9f9` directing/avatar
features and the coordinated provider-neutral worker parser:

| Method | Path |
| --- | --- |
| GET | `/api/health`, `/api/status`, `/api/projects` |
| POST | `/api/projects` |
| GET, PATCH | `/api/projects/{uuid}` |
| POST | `/api/projects/{uuid}/{plan,render,assets,cancel,duplicate,narrate,clone}` |
| DELETE | `/api/projects/{uuid}/voice` (no request body) |
| GET, HEAD | `/media/{uuid}/{filename}` |

UUIDs are lowercase canonical hexadecimal UUID-shaped segments. The filename
matches the worker's `[a-zA-Z0-9_-]+.(png|jpg|webp|mp4|wav|json)` grammar, including
actual render filenames. Only media permits a query, exactly `download` or
`download=1`. All other queries, percent encodings, dot traversal, duplicate
slashes, absolute URLs, CONNECT, arbitrary paths and methods are rejected.

Only the dedicated guest-supplied bearer, content type and a single validated
byte Range reach the Unix worker. Platform cookies, bridge/API tokens, forwarded
identity, proxy credentials and arbitrary headers are dropped. Media responses
preserve Content-Range, Accept-Ranges and Content-Disposition. All responses are
no-store; worker cookies, authorization and arbitrary response headers are not
exposed. Redirects and unexpectedly compressed responses fail closed.

JSON requests are limited to 65,000 bytes. Uploads accept exactly one non-empty
file of at most **50 MiB**, plus an optional `kind` of `media`, `logo`, `portrait`,
`voice` or `narration` (default `media`). A single optional `consent` field must
be exactly `true` or `false`; `portrait` and `voice` require `true`. Both the
broker and the coordinated worker enforce this before a complete upload can
be accepted. File-valued, duplicate or malformed consent is refused regardless
of field order. Incoming
multipart wire bytes, including an epilogue, are capped at **51 MiB** even with
chunked transfer. Multipart is re-encoded incrementally; no host archive, temporary
file or whole-upload buffer is created. Unsupported/duplicate fields and part
headers fail. Go's multipart parser also has its own bounded header allocation;
accepted part headers are capped at 8 KiB. An invalid upload never receives a
completed forwarded closing boundary. This alone is not a universal transactional
upstream guarantee: the coordinated worker validates the complete form and file
limit before asset writes, with its own truncation/duplicate/oversize regressions.

The latest-action and upload regressions include file-first/file-last ordering,
preserved payload bytes, explicit consent, incomplete closing boundaries on
rejection, and body-free voice deletion. The complete Motion broker suite passed
with the Go race detector. This source validation does not establish deployed
worker/template acceptance or migrate the existing Motion project.

JSON responses are limited to 16 MiB, media responses to 1 GiB, and all requests to
120 seconds. Both known and unknown body lengths are bounded. Streaming overflow
after response headers terminates the response; callers must treat a truncated
body as failure. The 1 GiB media cap is an explicit supported limit, not a claim
that arbitrary exports fit it. Closing an incomplete channel upload aborts that
stream without draining a malicious unfinished body or retaining its goroutine.

## Acceptance and remaining rollout work

[Sanitized source/test evidence](results/2026-09-25.json) records the two Linux
snapshots and final local parser regression separately.

Local tests cover exact routes and generation/app denial, credential filtering,
malformed/multiple ranges, 206 headers, streamed exact-50-MiB and one-byte-over
uploads, chunked/epilogue bounds, duplicate/truncated upload behavior, Unix-socket
cancellation, active revocation and four-request admission. A real authenticated
reverse channel carries the 50 MiB upload and media range; a sibling channel
cannot use its capability. API tests use persisted app/binding state, and guest
mux tests verify the fixed namespace alongside existing model/bridge services.

Before enrolling this actual app: deploy the reviewed worker listener and socket
mount, updated controller and guest template; verify authorized `/api/status` and
project read; then use an owned synthetic film for PATCH, multipart upload,
HEAD/range playback and download plus forbidden sibling/stale generation tests.
Do not use the app's local `/api/health` as proof: it does not contact its worker.
The offline broker requires the same explicit app selection and an authoritative
migration journal. Its named handler is bound to the frozen Docker source app,
exact target ID/template/domain/encrypted credentials and eligible journal phase
(staged/imported/verified). Phase or binding changes revoke prior requests; detach
and shutdown cancel them. Completed/rollback journals may retain the ordinary
reviewed broker for cleanup, but receive no Motion capability. Tenant environment
values cannot grant this authorization: a recognized worker configuration without
the exact app mapping is rejected at target configuration verification.

Both online config synchronization and offline verification require the actual
guest capability before applying the URL overlay. The config revision suffix is
acknowledged only after successful application and authenticated status. Tests
exercise disposable SQLite journals, a real authenticated reverse channel,
phase/sibling rejection, cancellation, failed config application and preservation
of encrypted original values. These are integration fixtures; actual deployment
and owned-film HTTP/upload/playback acceptance are still required. Do not disable
the mapping while leaving an app on Cube: perform the journaled Docker rollback
or an explicitly reviewed replacement configuration first. Revocation closes
access; it cannot turn the old Docker worker address into an authorized route.

The app's JSON projects, uploads, jobs and exports live separately at
`/opt/baarcha/motion-studio/data`, outside guest home archives. Preserve and back
up that service/data independently. Its 6 GiB/250% CPU limits remain part of host
capacity accounting, outside the four Cube guest slots. The installer must use
provider-neutral runtime APIs, never write a Docker workspace path for a Cube
app. Do not relax listing privacy or remix permissions as part of this change.

## New template derivation and activation sequence (not executed)

The eight installed READY templates predate this supervisor capability. Do not
edit their records, relabel an old template, or claim capability from a version
string. Retain their IDs/images for unrelated apps and rollback.

1. Freeze and test one runtime source commit containing controller, migration CLI
   and `runtimed` changes. Record commit, clean tree, source archive and binary
   SHA-256 values. Build from the reviewed credential-free **base** image, never
   an existing preset image (its workspace is already populated) or tenant disk.
   Supply its recorded immutable image ID/digest explicitly; do not use the
   Dockerfile's moving default. In an isolated build stage, the derivation is:

   ```sh
   : "${REVIEWED_BASE_IMAGE:?immutable reviewed base image required}"
   : "${CANDIDATE_TAG:?new candidate image name required}"
   docker build --file image/cube/Dockerfile \
     --build-arg BASE_IMAGE="$REVIEWED_BASE_IMAGE" \
     --build-arg RUNTIME_PRESET=react-vite \
     --build-arg CUBE_REVERSE_EGRESS=1 --tag "$CANDIDATE_TAG" .
   ```

   Bound build resources in the existing isolated build stage. Record the exact
   resulting image ID and registry manifest digest after transfer. Verify the
   image's `runtimed` binary hash, Node 22.21+ and absence of owner credentials.
2. After the parent hands over the worker (no other build/acceptance running),
   create **one new alias** using the same reviewed CLI options as
   `ops/cube/production-worker/register-templates.py`: immutable image digest,
   `--cpu 2000 --memory 2048 --writable-layer-size 10Gi`, ports 3000/3001/3031,
   probe 49983 `/health`, `--with-cube-ca=false`, `--deny-out-cidr 0.0.0.0/0`.
   Persist create/job metadata privately before watching; fail on an existing
   output or unfinished job. Do not rerun the eight-template registration script
   or change the default preset mapping. Confirm the stored template parameters
   and READY state; inspect rather than delete any failed job.
3. Deploy compatible controller/CLI code and the coordinated worker UDS listener
   under the existing release locks and private backups. Preserve the existing
   Docker TCP listener and worker data. Install the narrow read-only socket
   directory mount and exact app mapping. Verify listener DAC and worker bearer
   handling; no generic private-network allowance is added. Existing preview
   signing keys and reverse-broker gates remain mandatory.
4. An owned synthetic target using the **new exact template ID** must report
   `motion-worker-v1` through its authenticated control channel. Verify Node's
   fixed named URL, exact owner/generation authorization, worker `/api/status`,
   synthetic project creation/edit, exact 50 MiB upload and one-byte-over failure,
   HEAD/single-range video response and playback/download. Confirm the worker
   rejects incomplete/duplicate multipart before writes. Test a separate owned
   sibling denial and revoke the target channel. Delete only owned fixtures and
   independently confirm target absence/admission release.
5. Only then review Motion's journaled migration with this exact new template,
   including a scoped `/api/status`/project request **through the target** before
   cutover, final stable preview link and canonical app identity, encrypted-config
   fingerprint unchanged, and rollback to the retained Docker URL. Local app
   health alone is insufficient. Preserve external worker projects/media backup
   independently of the guest archive. Change no global template mapping until
   the rest of the fleet's separate acceptance is complete.

The source capability makes old templates fail closed; this sequence has not
been executed and does not establish global migration readiness or performance.
