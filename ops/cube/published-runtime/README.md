# Existing guest supervisor rollout

The shared file-path guard now permits explicit reads of `dist` while root
workspace listings and source archives still exclude build output. New images
built from this source include the fix. Existing microVM snapshots retain their
old supervisor, so the bounded operator below upgrades recognized idle guests
as they wake. It does not wake guests or start AI tasks.

Build the guest supervisor with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64
go build -trimpath -o runtimed ./cmd/runtimed` from `control-plane`, matching
`image/cube/Dockerfile`. Both the worker and guest enforce the same ELF guard before any quiescence:
Linux/amd64, executable type, load segment, and no dynamic interpreter. Also
verify `file runtimed` reports a statically linked executable before staging it. The controller uses a separate CGO
build because its SQLite driver requires it.

Install reviewed `guest.py`, `worker.py`, the compiled Linux `runtimed`, and a
root-owned `release.json` in `/opt/baarcha-published-runtime` on each **worker VM**.
Never run this on the shared outer B200 host. The release manifest pins
`sha256`, `bytes`, and an explicit `previous` SHA allowlist. Unknown supervisor
builds are skipped; do not extend that allowlist without reviewing their source
and configuration differences. Files and directory must be root-owned, with
scripts mode 0700 and manifest mode 0600.

`python3 worker.py --probe` reports only runtime IDs, supervisor hashes, and
whether a task is active. `--container <runtime-id>` limits an update to one
running guest. Without that option, it visits only currently running native
containerd tasks. Install the included service/timer after the canary succeeds.

The guest checks the reviewed hash, task state and existing workspace fence,
receives and verifies the entire binary before maintenance, then uses the
supervisor's atomic quiescence guard. A task accepted during transfer wins.
The update preserves application environment values and configuration revision
and uses the existing guarded configuration restart API. Existing empty
configuration receives an internal revision marker. Credentials never leave the
guest. A hard link retains the old binary without copying its data blocks;
failed verification attempts to restore it before releasing the owned fence.
That rollback requires a reachable supervisor: if the VM exits during reexec,
the retained binary alone cannot revive it. Preserve its disk for operator recovery.

The worker serializes updates with `flock`. Each execution has a 240-second
outer deadline, bounded output, and bounded internal readiness waits. Watch
`journalctl -u baarcha-published-runtime.service`; `updated`, `current`, `busy`,
and `unreviewed_binary` are explicit receipts. Investigate `failed` receipts.
Stop this rollout with `systemctl disable --now baarcha-published-runtime.timer`.
Stopping the timer does not revert already updated guests. To revert a reviewed
release, stage the retained old binary, a reverse hash allowlist, and
`"dist_status": [200, 404, 400]` in the rollback manifest (the old guard rejects
build reads with 400). Run the same guarded updater. This restores the supervisor
but disables automatic build collection until the forward fix is reapplied.

September 29 canary and Ooredoo/LevelUp updates preserved guest configuration
and runtime bindings. The older Motion Studio supervisor was deliberately
skipped; its separately verified production artifact can be imported through
`POST /v1/apps/{id}/published-build` with `Content-Type: application/zip`.

## September 29 incident

Both automatic updater timers are disabled following a VPS rollout failure. A
CGO-linked candidate was briefly staged; the updater previously checked its hash
but did not reject a dynamic ELF interpreter. MyHomeTroc reported current at
14:09 UTC and failed at 14:10 UTC; its native task disappeared. Its recovery disk
retains the prior static binary and workspace quiescence marker, consistent with
an attempted rollback that could not finish. The exact guest exit diagnostic
was not retained, so the rollout is the likely trigger, not a proven crash stack.

`test_supervisor_binary.py` covers static acceptance and rejection of the loader,
wrong architecture, shared-object type, absent load segments and malformed ELF.
Do not enable the timer until a bounded canary passes and every failed receipt
has been investigated. The guard is installed on both worker VMs.

## October 9 VPS continuation

The static `2c7e700` supervisor passed the VPS canary and sequential production
upgrades. The older `ea25000…` and Motion `b300f9…` builds were rejected before
mutation, then reproduced from `06ab5e4` and `d7ee603` respectively. Every byte
matches except Go's build identifier. Their existing configuration/quiescence
contracts remain compatible; both exact hashes were added to the VPS release's
previous-build list. Motion's update preserved configuration. See the source
comparison receipts in `../vps-capacity-20261008/results/`.

The fleet continuation preserves completed receipts and original failed attempts.
Its VPS-only timer installer requires completed fleet wake validation; the timer
has not yet been enabled. The B200 timer remains untouched and disabled.
