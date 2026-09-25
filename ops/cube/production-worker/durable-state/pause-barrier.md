# Pause commit candidate (0007)

This patch is a candidate, not an installed or power-loss-validated change. It
requires 0006's persistent XFS metadata configuration and synchronous CubeStore
writes. The currently installed worker is unchanged.

The pinned pause path ignored SyncByID errors, wrote rename-based metadata
without directory barriers, marked PAUSED before cleanup, and released quota
for PAUSING. Its event publisher also enqueued changes before committing their
metadata. A failed pause could therefore appear to release capacity.

The candidate orders the XFS path as follows:

1. Persist pause intent. If it fails, restore intent labels and return an error
   before pausing the guest.
2. Keep an in-memory publication fence throughout snapshot generation. APIs and
   heartbeat see PAUSING, and PAUSING remains fully charged.
3. Write sandbox/catalog metadata using file fsync, atomic rename and parent
   directory fsync. Preserve current disk and new artifacts on every failure
   after pause begins. No failed fsync authorizes artifact cleanup.
4. After the guest freezes, finalize the RAM/rootfs package. The pinned shim
   writes VMM config/state under `<MetaWork>/snapshot/` and its
   `metadata.json` directly in MetaWork (CubeShim/shim/src/sandbox/sb.rs,
   pause_vm_to_snapshot_inner). Cubelet finalizes that same tree as MetaDir. Fsync the current
   disk, captured rootfs and RAM regular files on the same reviewed XFS; fsync
   every bounded package metadata file/directory; then syncfs that filesystem.
5. Persist PausedAt synchronously while the in-memory fence still reports
   PAUSING. Only after that transaction succeeds may observers see PAUSED.
   Update events are dispatched after successful DB persistence.
6. Persist cleanup intent before destroying live resources. Report tombstone
   finalization errors instead of acknowledging success. A failed/pending pause
   or UNKNOWN binding retains CPU/RAM quota until explicit recovery/deletion.

The status fence is deliberately nonpersistent: the disk record contains
PausedAt only after snapshot barriers, while concurrent in-memory observers stay
fenced until the write succeeds. Restoring a durable paused record after restart
therefore does not depend on an ephemeral flag. On failure the live record is
marked recovery-required; failed persistence is logged and returned, never
converted into success.

S3 pause is rejected before mutation by this candidate. Only the deployed XFS
backend has a reviewed local barrier. Existing disk accounting still retains
logical writable/snapshot disk costs; this patch does not inflate disk quotas.
UNKNOWN bindings conservatively remain charged even when a missing task might
already have released physical memory. Releasing their reservation requires an
explicit recovery or deletion decision.

## Remaining durability gates

The native cubecow C header exposes create/activate/deactivate/export and engine
shutdown, but no reviewed per-engine flush API. Syncfs flushes kernel dirty data
and filesystem metadata; it cannot prove a native library has drained all
user-space buffers. The retained current-disk SQL recovery proof establishes a
separate concrete result; it is not proof of RAM checkpoint durability. A real
owned acknowledged-pause/whole-worker-loss/resume test is still required.

The Go barrier covers all files returned by the shim before committing PAUSED.
Optional patch0009 would additionally sync Rust SnapshotInfo::store before
returning. It is excluded from required0007 and the native Cubelet candidate,
and remains unbuilt/untested because the pinned Cargo dependency cache is absent.
The installed shim is unchanged. Go explicitly syncs every returned metadata file
and its parent directories before the paused database record is committed.

Master completes its pause binding only after Cubelet success and returns an
error if the SQL update fails (sandbox_resume_pause.go). Deployment must also
verify the actual Master database's durable commit settings. No source-level
Go test establishes storage-device power-loss behavior or a successful native
cold restart of a previously running guest.

Failed artifacts remain retained and consume storage. They must be surfaced for
operator review and counted by the existing free-space/admission guard, not
silently garbage-collected or treated as spare capacity.
