# Owned durable-worker acceptance preparation

This runner tests one synthetic node-postgres guest on the reviewed worker. It
performs no power operation, provider repair, network-policy change, backup
restore, production rollout, or tenant access. Preparation/build is authorized;
execution requires the root coordinator's explicit worker handoff after upgrade.
The clean, paused-loss and running-loss experiments use **different new guests and private stages**.
Earlier crash results and artifacts must remain untouched.

## Recorded execution

The corrected worker passed the fresh PG/Vite, clean paused reboot and acknowledged-pause
abrupt-loss checks. See [actual results and limits](results/2026-09-25-de3/README.md).
These records do not assert running-loss recovery or production coordinator acceptance.

## Build and review

Copy the current complete `control-plane/` source into a new isolated directory,
then run:

```sh
GO=/reviewed/go SNAPSHOT=/isolated/control-plane ./prepare.sh
cd /isolated/control-plane
go test -race -count=1 ./cmd/operator-clean-worker
go build -trimpath -o durable-worker-acceptance ./cmd/operator-clean-worker
sha256sum durable-worker-acceptance cmd/operator-clean-worker/*.go
```

The build-only Linux container must have no network, at most 2 CPUs/2 GiB, and a
bounded executable scratch tmpfs (512 MiB). The original crash helper is copied
by an explicit AST declaration allowlist. Four exact, asserted substitutions
change only the clean handoff and progress labels; an upstream helper change
fails preparation rather than silently changing those checkpoints. No original
crash source is edited. The local macOS build cannot run these Linux archive
APIs; Linux cross-compilation alone is not a passing execution test.

## 1. Clean pause/resume and worker reboot

After the upgrade, independently verify the reviewed executable/configuration,
worker machine ID, data UUID, all-state inventory zero, complete one-node scan,
zero Cube tasks and no template jobs. Root must retain exclusive fixture
ownership until cleanup; the runner additionally holds the shared outer and
worker fixture locks. One guest uses 2 CPUs/2 GiB, with direct egress denied and
authenticated reverse transport. No application/model external calls occur.

Create a **new** root0700 stage
`/opt/baarcha-bench/cube-crash-clean-REVIEWED_UNIQUE_SUFFIX` containing:

- `durable-worker-acceptance` and its reviewed SHA-256;
- exact `../crash/probe.mjs`;
- root0600 `postgres-lifecycle-report.json` from the complete successful PG
  fixture (all seven required boolean assertions must be true);
- root0600 `handoff.json`, created by the coordinator only after its checks:

```json
{"purpose":"DISPOSABLE_CUBE_CLEAN_HANDOFF","worker_machine_id":"<32 lowercase hex>","no_customer_guests":true,"previous_family_cleanup_verified":true,"expires_at":0}
```

Set `expires_at` to a Unix timestamp no more than 30 minutes ahead. Run on the
outer operator host, with private stdout/stderr redirection:

```sh
./durable-worker-acceptance run /opt/baarcha-bench/cube-crash-clean-REVIEWED_UNIQUE_SUFFIX
```

The runner creates exactly one owned guest, escrows its credentials privately,
installs the reviewed probe, commits baseline data, exports independent private
app/home backups, then commits **different latest** app/home/SQL markers. It
pauses and resumes the original guest and requires the actual app's SQL health
and exact latest marker in all three stores. It pauses again, requires the
original ID/resources and a complete single-owned-guest inventory with zero
active Cube tasks, and prints `CLEAN_REBOOT_READY`.

**Only `CLEAN_REBOOT_READY` permits the separate clean-powerdown step.** The
preceding `LATEST_MARKERS_READY` message expressly does not. No marker or backup
is rewritten after this checkpoint. Root reviews the exact QEMU identity and
performs the separately authorized clean powerdown/boot. After control-plane
readiness and identity verification, root writes a new0600 receipt:

```json
{"purpose":"DISPOSABLE_CUBE_CLEAN_REBOOT_COMPLETE","fixture":"<report.fixture>","previous_boot_id":"<report.worker_boot_before>","method":"operator-reviewed-clean-powerdown"}
```

The receipt filename is `clean-reboot-complete.json`. The runner waits at most
15 minutes, reacquires the worker lock, requires changed boot ID with unchanged
machine/data identities, requires the **same guest already paused**, resumes it
and verifies latest app/home/SQL again. Timings cover pause and resume through
actual SQL verification, not API response alone. Exact-owner deletion plus
independent GET404 and complete zero inventory are required for success.

A failed post-checkpoint run retains the owned guest and all evidence. After
review, `verify STAGE` resumes only verification, or `cleanup STAGE` performs
exact-owner deletion without claiming new durability acceptance. No bulk delete
or automatic restore is permitted. The overall runner deadline is 30 minutes.

This proves clean paused-guest data survival only. It does **not** exercise the
production controller stop/start coordinator, maintenance gate, controller DB
rebind, or a running worker crash. Those remain separate acceptance records.

## 2. Acknowledged pause followed by abrupt loss (patch 0007)

After clean acceptance and independent zero-inventory confirmation, use the
same executable with explicit `run-paused-loss` in a **new** root0700 stage
`/opt/baarcha-bench/cube-crash-paused-loss-REVIEWED_UNIQUE_SUFFIX`. Package the
same probe, successful PG report and reviewed binary. Its root0600 handoff is:

```json
{"purpose":"DISPOSABLE_CUBE_PAUSED_LOSS_HANDOFF","worker_machine_id":"<32 lowercase hex>","no_customer_guests":true,"previous_family_cleanup_verified":true,"expires_at":0}
```

Run:

```sh
./durable-worker-acceptance run-paused-loss /opt/baarcha-bench/cube-crash-paused-loss-REVIEWED_UNIQUE_SUFFIX
```

It commits baseline, exports the independent older backups, commits distinct
latest app/home/SQL, and then performs **the first and only pause since that
latest commit**. It does not resume before the loss: an earlier snapshot of the
same latest marker must not mask a broken final-pause durability boundary.
Only after Pause returns and exact paused metadata/zero running tasks are
confirmed does it print `PAUSED_LOSS_READY`. Root then performs the separately
reviewed **abrupt** loss on that exact worker and boots it. After readiness,
root creates mode0600 `paused-loss-complete.json`:

```json
{"purpose":"DISPOSABLE_CUBE_PAUSED_LOSS_COMPLETE","fixture":"<report.fixture>","previous_boot_id":"<report.worker_boot_before>","method":"operator-reviewed-paused-power-loss"}
```

Verification requires the unchanged guest already paused, resumes through real
SQL health and latest app/home/SQL comparison, then exact deletion/GET404 and
zero inventory. Clean and paused-loss handoffs, stage prefixes, receipt names,
receipt purposes/methods and saved modes cannot be interchanged. Recovery verbs
are `verify-paused-loss` and `cleanup-paused-loss`; the unsuffixed verbs remain
clean-only. This proves acknowledged-pause durability against abrupt loss; it
does not prove survival while the guest is running.

## 3. Separate running abrupt-loss check

Only after the paused-loss guest is confirmed deleted and inventory zero, use the
**unchanged** `../crash/main.go` coordinator in another new
`/opt/baarcha-bench/cube-crash-...` stage. Follow its README exactly, including the
`DISPOSABLE_CUBE_CRASH_HANDOFF` and original `CHECKPOINT_READY` marker. Do not
pause/checkpoint/export the guest after its latest commit. Its baseline exports
are deliberately older and cannot satisfy the latest-marker assertion.

Before the separate root-controlled power loss, capture read-only metadata for
the exact escrowed ID inside the worker using the reviewed recovery tools:

```sh
python3 metadata_capture.py --sandbox-id EXACT_OWNED_ID --output NEW_PRIVATE_BEFORE_DIR
```

Copy that private metadata escrow outside the worker and record file hashes.
Do not print it: it contains guest bindings/credentials. Record the installed
Cubelet/Master/API hashes and current-disk path, volume identity and generation
from the validated plan. This is metadata escrow, **not** a disk backup or a
claim of crash-consistent bytes.

After root's single abrupt loss and control-plane restart, preserve ordinary API
state and repeat the exact-ID read-only metadata capture to a new directory.
Verify the original metadata/current-disk identity is retained and is not a
substituted old template/snapshot. The original crash coordinator may connect
only a legitimately paused original guest and succeeds only if the latest
app/home/SQL values survive without another commit, import, repair or recreate.

A truthful runtime error with retained current disk is **not** a native app
recovery pass. Preserve that failure and all artifacts; verify ordinary API
responses neither falsely report running nor discard the failed guest's data.
If native connect cannot recover, the separately reviewed isolated rescue and
recovery-journal procedure must demonstrate latest data on a new replacement.
Label native recovery and replacement recovery separately. Do not attempt
unreviewed automatic fallback or erase the retained original disk.

## Remaining execution gates

The candidate must be installed and root must hand off the worker. The runner
has no automatic power implementation and intentionally cannot prove the
production stop/start coordinator. A prior PG lifecycle report is a packaging
precondition, not a fresh candidate result; root may rerun the existing PG
fixture first. Its isolated admission profile should be bounded to the tested
four slots (the PG scenario itself owns at most two guests), without claiming
12-slot readiness. Failed native error-state recovery may require the existing
separate rescue/journal path, whose prior pass does not prove this new candidate.

## Fresh four-slot PostgreSQL and Vite prerequisite package

`prepare-api.py` copies the existing full PG and Vite acceptance fixtures into an
isolated control-plane snapshot, changing only their explicit admission limit
12→4 and adding `admission_max_active: 4` to the new reports. It also copies the
existing runner, reload probe and private preflight, strengthening the latter to
require a complete one-node CLI inventory. Original source fixtures and all
historical results are untouched. Preparation refuses existing copied fixtures
or an existing package directory.

```sh
python3 prepare-api.py --snapshot /isolated/control-plane --package /new/private/package
cd /isolated/control-plane
go test -race -run '^TestOperatorAcceptanceCleanupVerifiesRemoteDeletion$' ./internal/api
go test -c -o /new/private/package/api-acceptance.test ./internal/api
cd /new/private/package
sha256sum api-acceptance.test > binary.sha256
```

The package must also contain the corresponding source snapshot under
`source/control-plane/`, including migrations and the API directory. Keep normal
repository fixtures (docs, traefik, image/templates) under `source/` as well.
No `.env`, credentials, user data or tenant source belongs in that package.

After the worker has booted and root has independently reviewed readiness, copy
this complete package into a **new root0700 worker stage**. Root writes the
one-use0600 handoff described in `../README.md`; the runner independently checks
private API credentials, security-family cleanup marker and empty all-state
inventory under the shared fixture lock. Root can then explicitly invoke:

```sh
systemd-run --unit=UNIQUE_PG_FIXTURE_UNIT --wait --collect \
  -p CPUQuota=200% -p MemoryMax=2G -p RuntimeMaxSec=14min \
  /root/NEW_PRIVATE_STAGE/run-api.sh postgres pg-current-01
```

The wrapper sets `CUBE_POSTGRES_FUNCTIONAL=1`,
`CUBE_POSTGRES_TEMPLATE=tpl-ce1ee426e686460bbc8c3bfc`, and
`CUBE_POSTGRES_STAGE=STAGE/runs/postgres-pg-current-01`; its executable runs from
`STAGE/source/control-plane/internal/api`. These settings are fixtures only,
not production gates. The real API key remains in the existing root0600 worker
file `/root/cube-production/test-secrets.json`, never in the invocation or logs.

After PG cleanup is independently verified, root may create a **new** one-use
handoff with family `reload`, run ID `vite-current-01`, and invoke the same wrapper
with `reload vite-current-01` in another unique transient unit. It sets
`CUBE_RELOAD_FUNCTIONAL=1`, template `tpl-98b45d63cfcc48c5b6ba9104`, and a distinct
run directory. It uses the same four-slot binary and exercises actual Vite source
and environment reload behavior. These bounds constrain the fixture controller;
the owned guests separately retain their reviewed two-CPU/two-GiB allocation.

The PG result is valid for durability packaging only when its seven original
functional assertions plus owned deletion are all true. Copy that fresh report
as mode0600 into each new clean/paused-loss/running-loss stage. A skipped opt-in
unit or successful compilation does not meet that precondition.
