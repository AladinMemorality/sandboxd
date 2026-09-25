# Canonical operator fixture for paired restore

Root executed the initial owner/app phases on2026-09-25. Synthetic owners103
and104 were committed with notification rows suppressed; the first app request
returned400 because the fixture incorrectly used template directory
`node-postgres-standard` as the API preset. Read-only DB/API evidence confirmed
no app existed. A reviewed one-use `resume-rejected-app` action preserved the
original failed intent and owners, corrected the API preset to `node-postgres`,
and created app01M3CZB4HXT2Y8HP8CEY75PCWY. No sandbox, credit, task or application
SQL rows were created in these phases. See `prepare-result-20260925.json`. It intentionally uses the canonical controller and real
platform ownership; an isolated fixture database cannot replace that proof.

Before any action, root must complete the real empty host enrollment cycle,
deploy storage migration34/controller enforcement and rebuild/pin the installed
`baarcha-cube-worker-stop` and `baarcha-cube-worker-start` from that source. The
review02 coordinators predate migration34. Keep global Cube routing disabled.

`run.py` holds the four existing outer deployment/operator locks and the actual
nested acceptance lock through each phase. It imports only the SHA-pinned
`NestedLock` implementation from the sibling cutover/enrollment source. Deploy
this small directory with that same relative source layout. It does not install
units, change routing, restart services, pause guests or delete any resource.
An unrelated holder causes immediate refusal. Never run it during enrollment
or another acceptance fixture.

The reviewed config is root0600 with a fresh canonical root0700 stage. Populate
all identities from actual installation receipts; `config.PREPARED.json` is
intentionally invalid and non-authorizing. `host_cycle_receipt` is the actual
completed enrollment's `complete.json`, independently SHA-pinned. Original
helper's immutable enrollment SHA, current worker boot and newly rebuilt
coordinator hashes must match. Refresh the controller pin only after the
separately reviewed exact-app operator allowlist deployment.

Run one action at a time with the exact reviewed `fixture.mjs` SHA:

```sh
python3 /reviewed-source/ops/cube/backup/canonical-fixture/run.py \
  --config /private-stage/config.REVIEWED.json \
  --action prepare --execute-sha256 REVIEWED_FIXTURE_SHA256
```

1. `prepare`: Cube must be disabled. Create two synthetic owners/sessions in one
   transaction; delete welcome/slack notification queue entries before commit.
   Create only a canonical app row using the real loopback API with
   `external_user_id=baarcha:<owner>` and the API preset `node-postgres` (template directory: `node-postgres-standard`). Save the returned
   app ID, then stop. It must not create a Docker fallback sandbox.
2. Root separately configures exactly that one app in Cube's allowlist, with
   global rollout off, reviewed template and four-slot2CPU/2GiB/storage contract.
   Update the root-private controller identity/config; no broad allowlist.
3. `create`: require the exact reviewed app and no existing sandbox. Create once;
   preserve its actual Cube provider/template/config binding. A Docker result or
   ambiguous acknowledgement is retained as a failure, never auto-deleted/retried.
4. `fund`: insert exactly1000 millimes (1TND) of approved operator test credit
   under a unique run reference; verify actual ledger owner/kind/amount/balance.
   No default beta grant, notification, payment or other account is involved.
5. `task`: require no prior task or model usage and the unchanged1000 balance.
   Bind the same per-app owner bridge-token contract as platform `bridgeEnvFor`;
   submit one real canonical task with explicit120-second runtime limit,
   continue=false and an independent180-second cancellation watchdog. The
   platform task route defaults to1200 seconds and cannot accept this explicit
   per-call bound, so this fixture intentionally uses the canonical runtime task
   API. No global provider credential enters the guest. The existing model
   gateway still authenticates and meters the exact owner. The credit grant is
   **not a hard total spend cap**. No paid task retry occurs.
6. `verify`: independently read the authored app/home marker endpoint, relative
   symlink and0750 executable mode; acknowledge two actual PostgreSQL inserts and
   read both back, including the latest row. Require real task success,
   checkpoint and nonempty events, then successful measured owner model usage.
   Capture the real page via the existing shared screenshot service and check
   owner200/foreign404/anonymous404 on the exact scoped cover bytes. Require
   foreign capture404 and signed-out401. It creates a private draft cover without
   publish, unlisted transitions, remix or snapshot. The image remains private.
7. `inspect`: read retained phase/IDs without retrying mutations. Ambiguous state
   requires root's evidence-based reconciliation before further execution.

The stage contains owner sessions and operational evidence and stays private.
Before every mutation, journal intent is durably replaced and fsynced. A failed
request leaves its intent; subsequent automatic actions refuse. Mutations are
never automatically retried. A process crash also leaves `running.lock`: inspect
the exact PID/request/provider state before root removes that stale lock. Do not
clear an intent merely because enough time passed. A lost task acknowledgement
still has the server-enforced120-second runtime limit; resolve its canonical
history before continuing. Cancellation acknowledgement alone is not terminal
proof.

The baseline records actual existing sandbox IDs/providers and reports legitimate
new additions. It does not assume66 or67 forever or delete concurrent legitimate
projects. The script permits at most its one canonical Cube binding; the operator
must separately confirm provider all-state inventory has no unbound guest or job
before the first create. Existing admission/recovery ambiguity is refused.

`verification.actual_browser_visual_review`, `.backup` and `.restore` remain
false. A root reviewer must inspect `private-capture.jpg` or the actual owner page
for rendered content; JPEG bytes plus HTML are not a browser-render assertion.
There is no automatic pass for full paired backup or restored application state.
Retain this fixture and its checkpoint until the actual paired restore test.

The installed backup helpers have a receipt; commit666ed10 records actual
synthetic129MiB multipart upload/fullGET/Mac GPG stream/strict restore. Full
production pair capture is still required. After planned disk expansion the
capture reserve is568GiB plus role archives, not a guarantee of physically
backed thin capacity. Keep the original running while sealing, uploading,
reading back and restoring disk artifacts. The40GiB original and40GiB isolated
clone cannot overlap on the62GiB host: use a separate controlled stop window
for clone app/home/history/checkpoint/latest-SQL verification, then shut down the
clone and reconcile the original. No long offline encryption/upload window.

Local source-only checks:

```sh
node --test ops/cube/backup/canonical-fixture/test_fixture.mjs
python3 -W error::ResourceWarning -m unittest discover \
  -s ops/cube/backup/canonical-fixture -p test_run.py
```

Eighteen Node tests cover phase ambiguity, exact provider/owner scope, bounded
response memory, cancellation/checkpoint behavior, runtime timeout and private
image ACLs; four Python tests cover real local flock contention and refusal.
These mocks/local files do not prove a live API, paid task, production grant,
rendered page or restored database. The real owner/app preparation was executed
separately by root; guest creation, funding, task and restored-application proof
remain separate acceptance gates.

`resume-rejected-app` is confined to the recorded run6ff432593177fb12, owners103/104
and original pending timestamp. It requires retained definitive400 evidence plus
zero current DB and owner-API app rows, and independently checks owner identities.
It records the reconciliation before one corrected POST; a second response loss
remains pending and cannot be retried by this action. It never recreates owners.
