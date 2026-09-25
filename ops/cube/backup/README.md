# Post-cutover Cube backups — inert candidate

This tool has not been installed, run against the worker, or scheduled. It never
stops a service, pauses a guest, changes a unit, overwrites a disk, deletes a
backup, or boots a restore. `worker-exec` is an explicit startup wrapper intended
for a separately reviewed unit change. Production activation remains gated on
an actual production-shaped encrypted backup and isolated application restore
evidence. A tiny synthetic GPG/qcow2 fixture has passed; its limits are below.

## What a recoverable set contains

A single cold generation must include both standalone worker qcow2 disks, the
controller SQLite database including migration/admission state, its encryption
key and full configuration, worker credentials/configuration and launch unit,
a consistent platform PostgreSQL dump, library snapshots, retained Docker
rollback homes/history/recovery archives, and an authoritative pause receipt.
Keys/configuration belong inside the encrypted backup. The **backup recipient’s
private key must remain off-host**, independently recoverable; the backup host
receives only the verified public key and exact primary fingerprint.

The capture copies full current Cube data. Old Docker archives alone cannot
protect edits or database rows written after cutover. The root disk is on RAID1;
the 320-GiB data disk is on unmirrored NVMe. A cold copy on the host’s separate
RAID protects against losing that NVMe, but not theft, root compromise, fire or
loss of the whole server. An off-host destination is still required. Do not
assume the laptop’s 60-GiB free space can hold this growing fleet.

## Consistency and availability

1. Apply the reviewed traffic/admission fence and drain all coding tasks,
   ordinary writes, streams and provider jobs. Stop uncontrolled/direct API
   writers. Gracefully quiesce databases, then pause every current Cube guest.
2. Record a fresh provider inventory with **all** runtime IDs paused and no
   provider jobs. Stop the controller, then gracefully shut down the worker OS.
   A killed QEMU process or a failed power-off is not this procedure. Preserve
   the observed shutdown/flush evidence separately; do not manufacture a receipt.
3. Capture under both exclusive lifetime locks. The script verifies an inactive
   worker unit configured to use this exact startup wrapper; the controller uses
   its existing maintenance lock. Native `/proc` FD inspection rejects another
   process using any source. Disable legacy/manual launch paths: cooperative
   locks do not constrain an administrator bypassing the protocol.
4. The tool converts each standalone disk into a fresh private directory,
   compares guest-visible disk content, uses SQLite’s backup API, hashes the
   complete file set and atomically publishes it. Sources are never changed.
   Incomplete private staging remains on failure for explicit review.
5. Only after successful capture may the coordinator restart the original
   worker/controller, verify readiness and remove the traffic fence. Encryption
   and transfer operate on the immutable staged copy after service resumes.

Cold-copy downtime includes copying **and** validation, not just guest pause.
No timing has been measured. As an illustration, reading 200 GiB once at
250 MiB/s already takes about14 minutes; conversion, comparison and other I/O
add time. This is a baseline/emergency mechanism, not a promise of unobtrusive
nightly backups. The tool conservatively requires free capture space for both
virtual disk sizes plus artifacts; encryption and restore need additional space.
There is no automatic retention pruning.

## Configuration and commands

Before first use, independently review a systemd `ExecStart` equivalent to:

```text
/usr/bin/python3 /usr/local/libexec/baarcha-cube-cold-pair.py worker-exec --lock-file /opt/baarcha-cube/worker-01/backup.lock -- /usr/bin/qemu-system-x86_64 EXISTING_REVIEWED_ARGUMENTS
```

The wrapper holds a shared lock for QEMU’s lifetime; capture requires exclusive.
Never unlink or replace a live lock inode. This repository does not install that
unit or wrapper. Use the same installed script path for capture so the unit check
can identify the startup protocol.

A private JSON configuration requires `root_disk`, `data_disk`, `database`,
`worker_unit`, `worker_lock`, and `artifacts`. `artifacts` must map each of these
roles to a completed, closed regular file: `controller-key`, `controller-config`,
`worker-config`, `worker-launch`, `platform-db`, `library`, `rollback`, and
`pause-receipt`. Directories must first be archived consistently by the approved
coordinator. Review their contents and completeness; a filename is not proof of
a usable PostgreSQL dump or complete rollback home archive.

The pause receipt format is:

```json
{"version":1,"generated_at":"UTC ISO8601 timestamp","provider_jobs":0,"guest_states":{"EXACT_RUNTIME_ID":"paused"}}
```

It must be observed within ten minutes before capture, match exactly the SQLite
Cube binding set, and accompany zero active/pending admission and coding tasks.
The script validates this evidence; it does not independently interrogate a
powered-off worker or assert a guest shutdown occurred.

All destination parents must already be owner-only0700 real directories; every
destination is new. Review the paths before manually running:

```sh
python3 cold_pair.py capture --config /private/capture.json --output /private/generation
python3 cold_pair.py seal --capture /private/generation --recipient-key /private/backup-public.asc --fingerprint EXACT_UPPERCASE_PRIMARY_FINGERPRINT --output /private/generation.tar.gpg
```

Transfer the ciphertext to independent storage and record its SHA256 in trusted,
independently retained metadata. Encryption integrity does not authenticate the
backup creator; verify that trusted hash before decrypting a received bundle. On the
independent restore machine, use standard GPG with the off-host private key:

```sh
umask 077
gpg --output /private/decrypted.tar --decrypt /private/generation.tar.gpg &&
python3 cold_pair.py restore --archive /private/decrypted.tar --output /private/recovered
```

Run restore only after GPG exits successfully; failed decryption may leave an
untrusted partial plaintext file, which must not be restored.

`restore` accepts only the exact regular-file set, rejects links/traversal,
checks all hashes and SQLite integrity, and runs non-repairing `qemu-img check`.
It does **not** start a VM or claim application recovery. An isolated restore
must then boot this matched pair with independent management addresses and no
production routing; validate canonical app bindings, owner data/history/config,
latest committed database rows and application readiness. Never run the restored
controller beside production with its original routing or credentials.

## Ongoing backup path and remaining gates

Periodic copies have a nonzero recovery-point interval. A write acknowledged
after the latest completed backup can be lost if the source disk fails. Backups
also do not repair the currently unproven cold restart of a **running** nested
VM after whole-worker loss. Those guarantees need separate runtime recovery and,
if zero data loss is required, synchronous durable replication.

QEMU offers point-in-time `blockdev-backup` and multi-disk transactions; a tested
coordinator could shorten the maintenance window and perform bulk copy later.
It still needs validated guest/database quiescence, metadata alignment, job
failure handling and restoration. Do not copy an active qcow2 directly.
See [QEMU live block operations](https://www.qemu.org/docs/master/interop/live-block-operations.html)
and [qemu-img safety and comparison](https://www.qemu.org/docs/master/tools/qemu-img.html).

Pinned Cube supports S3-backed snapshot paths, but the existing MinIO is on the
same worker. Selecting `s3` is not proof of an independent backup. Our bounded
admission client currently refuses memory-snapshot creation. An off-host object
store, reviewed authenticated lifecycle integration, retention and complete
restore test are required before choosing that approach.

Nine local tests cover busy/live/unfenced refusal, mismatched pause inventory,
ambiguous admission, exact public-key identity/private-key rejection, tar
traversal/oversized extension headers, non-overwriting publication, tampering, and a real SQLite plus ordinary-file round trip. The unit suite mocks QEMU commands. The separate resource-limited
[`live_fixture.py`](live_fixture.py) subsequently passed with actual QEMU8.2.2 and
GPG2.4.4 on two synthetic32-MiB qcow2 disks; see
[`fixture-result-2026-09-25.json`](fixture-result-2026-09-25.json). Only the
nonexistent synthetic worker unit check was replaced; real locks, PID descriptor
checks, SQLite backup, qcow2 check/convert/compare, encryption/decryption, strict
restore, recipient checks and tamper refusal executed. Latest synthetic disk
bytes and a committed SQLite row survived restoration. The fixture private key
was disposable and generated on the test host; it proves crypto interoperability,
not production off-host custody. All synthetic disks, plaintext and keys were
removed; the2CPU/1GiB isolated-network service was collected. No real worker was
stopped or booted, and no application restore or whole-fleet timing is claimed.

The installed GPG can emit `DECRYPTION_OKAY` and `GOODMDC` before reporting a
final AEAD tag checksum failure. The process exits nonzero: **always require
exit0**, never a status-string match alone, before restoring decrypted data. Standard GPG public-recipient
encryption is used, with no bespoke cryptography. Read
[GPG commands](https://www.gnupg.org/documentation/manuals/gnupg/GPG-Commands.html).

## Off-host recipient and object-store verification

The coordinator generated the dedicated production recipient on the operator's
Mac, protected its secret key with a random passphrase, and transferred only the
public recipient to the VPS. Fingerprint:
`25017F865BE80AA7E7E8C8925C595C2637931730`. The public-only host keyring encrypted
a synthetic verification payload; the existing backup-prefix credentials wrote
and retrieved its ciphertext from the independent object store. Off-host
decryption exited successfully and matched the original bytes exactly. See
[`offhost-recipient-proof-2026-09-25.json`](offhost-recipient-proof-2026-09-25.json).

The encrypted verification object is deliberately retained. No IAM policy or
delete permission was changed. This proves recipient custody and transfer for a
small payload; a full worker backup, application restore, scheduled backup and
redundant recovery-key custody remain unverified. The private key and passphrase
are outside the repository and have never been sent to the VPS.
