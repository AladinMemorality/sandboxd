# Next execution steps — root coordinates every live transition

The helpers never start a service or alter a lifecycle gate. These steps are
prepared commands, not recorded execution. Keep global Cube routing disabled.

1. **Empty host enrollment passed on September25.** The actual controller-fenced
   stop/boot/startup-reconciliation cycle completed at16:08UTC, including the
   documented retained-lock recovery continuations. See
   [actual enrollment evidence](../cutover/enrollment/actual-enrollment-2026-09-25.json).
   The deployed controller includes the additive migrations through34. Preserve
   its actual DB/key/config/image and require a new genuine scoped traffic/direct
   writer drain and real stop/start receipts for the backup cycle. Do not rerun
   the historical empty-enrollment runner against changed production identities.
2. **Use the existing canonical operator fixture.** The corrected API creation
   succeeded for app `01M3CZB4HXT2Y8HP8CEY75PCWY`, owner103, foreign owner104.
   See [actual prepare result](canonical-fixture/prepare-result-20260925.json).
   Do not create another app or rerun owner preparation. The API preset key is
   `node-postgres`; `node-postgres-standard` is the image template name and was
   rejected before any app write. No sandbox/task/credit was created by prepare.

   Root separately reviews a manual immutable-image Compose override with
   `SANDBOXD_CUBE_ENABLED=true`, `SANDBOXD_CUBE_ROLLOUT=allowlist`, and
   `SANDBOXD_CUBE_APP_IDS` equal to **only** that returned ID. Preserve the actual
   private management relays, exact eight-template/four-slot contracts and
   reviewed reverse policy. `POST /v1/apps/{id}/sandbox` with
   `{"runtime_preset":"node-postgres"}` then creates its provider binding.
   Existing customer Docker rows remain Docker. Ordinary release automation
   must be frozen during this temporary phase: `deploy-project-x.sh` preserves
   accepted global configuration and intentionally refuses an enabled allowlist
   deployment. Do not weaken that guard or fabricate global attestations.
3. **Record application-level expectations before capture.** In this owned
   fixture only, complete a bounded task and retain its canonical terminal task
   ID, event/result digests and any checkpoint reference. Write distinct app and
   nonworkspace-home markers and commit a unique PostgreSQL row. Independently
   read and retain all expected values/digests privately. An empty worker or
   fixture-only SQLite substituted for the real controller cannot prove this
   gate. Wait for all task completion and writer acknowledgements before drain.
4. **Perform the second genuine coordinator cycle and capture.** Prepare the
   closed role artifacts listed below. Fence/drain again, stop the real controller
   with restart disabled, and use the supervisor to obtain actual paused proof,
   retained component shutdown, QEMU exit and clean receipt. Within the receipt's
   ten-minute admission window, run the existing cold capture tool under its
   exclusive worker/controller locks. Capture/compare/hash finishes before
   restarting the original. No live qcow copy, forced poweroff or invented pause
   receipt is accepted. Resume/reconcile the original before encryption/upload.

## Completed role artifacts required by capture.json

| Role | Concrete required contents |
|---|---|
| controller-key | Actual `/var/lib/sandboxd/secrets.key`, closed regular file |
| controller-config | Exact controller image recovery reference/export, real config/environment/Compose overrides, source/migration hashes and canonical path inventory; secrets stay encrypted |
| worker-config | Installed nested/outer config, metadata-root and patch manifests, credentials and reviewed templates, plus all exact hashes |
| worker-launch | Exact outer unit/helper/QEMU hash and args, seed image, operator access recovery inputs, lifetime-lock contract |
| platform-db | Consistent custom-format PostgreSQL dump with successful dump exit and independently checked contents; platform writes fenced for the aligned generation |
| library | Consistent complete library snapshot artifact tree and metadata needed by published IDs |
| rollback | Retained Docker owner homes/history, migration/recovery archives and journals, required volume/config mapping; a live tar is insufficient |
| pause-receipt | Actual fresh coordinator pause proof matching canonical Cube runtime bindings |

Actual `controller.sqlite` is copied by the capture tool from the real database.
The role archives do not replace that field. Unknown/incomplete role contents
block capture even if a regular file happens to exist at the configured path.
Do not stop active customer tasks to manufacture an idle inventory.

## Bounded commands after those gates pass

Use new unique job names; paths below are an example generation, not existing
receipts. The helpers are installed under
`/opt/baarcha-cube/backup-tools`; the real synthetic129MiB multipart/full-readback,
off-host GPG pipe and strict disk/SQLite restore passed (see
[multipart evidence](multipart-transport-result-2026-09-25.json)). This is not a
full-worker capture. Root creates root0700
`/opt/baarcha-cube/backup-generations`. Configs are root0600. The fixed receiver
expects `stream_restore.py` at the former path. No helper is installed by Git.

```sh
python3 /opt/baarcha-cube/backup-tools/cold_pair.py capture \
  --config /PRIVATE/capture.json \
  --output /opt/baarcha-cube/backup-generations/pair-01-capture
# Restart and reconcile the original through genuine lifecycle receipts here.
python3 /opt/baarcha-cube/backup-tools/cold_pair.py seal \
  --capture /opt/baarcha-cube/backup-generations/pair-01-capture \
  --recipient-key /opt/baarcha-cube/recovery-recipient-20260925/recipient.asc \
  --fingerprint 25017F865BE80AA7E7E8C8925C595C2637931730 \
  --output /opt/baarcha-cube/backup-generations/pair-01.tar.gpg
```

Retain the small seal sidecar and its hash independently on the operator Mac
**before** trusting a later readback. Encryption does not authenticate its creator.
The standard production recipient is public-only on the VPS; neither the secret
key nor its passphrase is uploaded. The remote upload config has exactly:

```json
{"version":1,"manifest":"/opt/baarcha-cube/backup-generations/pair-01.tar.gpg.manifest.json","output":"/opt/baarcha-cube/backup-generations/pair-01-readback","bucket":"REVIEW_EXISTING_BACKUP_BUCKET","prefix":"REVIEW_EXISTING_BACKUP_PREFIX","readback_only":false}
```

Populate bucket/prefix from the existing reviewed backup configuration, not a
new destination or IAM policy. Invoke with the existing operator environment:

```sh
/opt/baarcha/node22/bin/node --env-file=/opt/baarcha/landing.env \
  /opt/baarcha-cube/backup-tools/offhost_store.mjs /PRIVATE/upload.json
```

The helper uses sequential128MiB parts and leaves exact multipart IDs in its
private journal on failure. Do not retry an uncertain completion as a new upload:
use a new output directory and `readback_only:true` with the same trusted seal
manifest. No object or unresolved multipart upload is automatically deleted.

On the Mac, create a private config for `stream_restore.py local --config FILE`:

```json
{"version":1,"ssh_control_path":"/PRIVATE/EXISTING_MASTER","source_ciphertext":"/opt/baarcha-cube/backup-generations/pair-01-readback/readback.gpg","ciphertext_sha256":"REPLACE_WITH_INDEPENDENTLY_PINNED_SEAL_HASH","ciphertext_bytes":0,"remote_job":"/opt/baarcha-cube/backup-generations/pair-01-plaintext","max_plaintext_bytes":0,"gpg_home":"/PRIVATE/OFFHOST/gnupg","passphrase_file":"/PRIVATE/OFFHOST/recovery-passphrase","evidence":"/PRIVATE/NEW/stream-evidence.json","timeout_seconds":86400}
```

The zero sizes are deliberately invalid. Set the exact ciphertext length and a
conservative plaintext ceiling from the immutable capture file sizes plus tar
headers/padding, at most1TiB. The receiver requires free space for that ceiling
plus64MiB. It streams through bounded pipes and stores no full archive on the Mac.
The off-host secret-key path belongs to root's private operator plan, not a host
environment or customer sandbox. Only after the helper reports success run:

```sh
python3 /opt/baarcha-cube/backup-tools/cold_pair.py restore \
  --archive /opt/baarcha-cube/backup-generations/pair-01-plaintext/decrypted.tar \
  --output /opt/baarcha-cube/backup-generations/pair-01-restored
```

## Isolated application restore acceptance

Boot **separate writable copies** of the restored matched pair, with a unique
QEMU name/socket, distinct loopback management/API/proxy ports, restrictive user
networking and no host directory shares or production routes. Retain the immutable
validated restored pair. Review host memory before adding a40GiB worker clone;
the current62GiB host cannot safely run the40GiB original and40GiB clone
together. Serialize their boot windows: upload, readback, decrypt and strict
offline restore while the original is running; stop the original only for the
separately coordinated application-proof window. Do not automatically start
the restored production controller, which has Docker identities and jobs for the
whole fleet. Use a narrow offline verifier against the copied SQLite/key and
only the owned synthetic provider in the isolated worker.

Require persistent metadata and exact old canonical app/sandbox/provider binding,
credential decryption and authenticated readiness; ordinary resume must preserve
the independently recorded app/home markers, terminal task event/result history,
checkpoint reference and latest committed PostgreSQL row. Hashes/qcow checks alone
do not prove these. Keep actual evidence and cleanup only exact owned clone/fixture
resources after review. Generate monitoring copy/restore receipts only from
successful observed outputs, never by setting all booleans true in advance.

The September25 19:08UTC observation recorded1,553,528,197,120bytes free on
outer RAID,416,053,002,240bytes on NVMe, about24GiB of Docker workspaces and
4.1GiB of library data. Recheck authoritative space before each stage.
Capture reserves both virtual disks (120+448=568GiB) plus roles. Budget additional
ciphertext, full readback, plaintext tar, restored pair and writable restore copies.
No fixed compression ratio or downtime is promised. The Mac's55GiB free space is
not used as a full-archive cache. Cold backups leave a recovery-point interval;
running-loss recovery and redundant off-host key custody remain separate gates.

Concrete role preparation and observed Docker/PostgreSQL consistency limits are
in [ROLE-COMMANDS.md](ROLE-COMMANDS.md).
