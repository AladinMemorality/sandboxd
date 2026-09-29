# Existing guest supervisor rollout

The shared file-path guard now permits explicit reads of `dist` while root
workspace listings and source archives still exclude build output. New images
built from this source include the fix. Existing microVM snapshots retain their
old supervisor, so the bounded operator below upgrades recognized idle guests
as they wake. It does not wake guests or start AI tasks.

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
failed verification restores it before releasing the owned fence.

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
