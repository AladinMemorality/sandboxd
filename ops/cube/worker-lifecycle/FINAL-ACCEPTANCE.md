# Final enrollment and recovery acceptance — prepared, not executed

This sequence is for root's coordinated maintenance only. The host source gate
remains false. It does not authorize service changes, customer migration, or
fabricated receipts. Production currently has 66 Docker projects; refresh the
canonical inventory under the final admission fence rather than freezing that
number into a migration plan.

## Prerequisites before final collection

1. Install the corrected Cubelet candidate through the reviewed binary-only
   procedure after graceful escrow and held reboot. Record its **full actual
   SHA256**; a shortened `de3bd4…` label is not an enrollment value. The actual
   containerd plugin must use its reviewed durable root, not merely accept a
   TOML key. Inspect its open metadata DB FD on `/data`, device identity and
   actual metadata after a clean reboot. Directory existence alone is not proof.
2. Account for retained disks and all-state provider inventory. Record native
   MainPID/executable handoffs for all five services. Re-run ownership inventory
   after installation; `Cubelet/dynamicconf/conf.yaml` and its directory are
   mandatory. Preserve the reviewed four-slot controller contract and actual
   worker quota; do not replace either with template defaults.
3. Verify the exact existing registry full ID has `always` restart policy,
   reviewed image, retained mount and loopback5000 mapping. Observe more than ten
   seconds of successful runtime and a successful registry `/v2/` check. Docker
   boot remains behind root's maintenance hold until explicitly released.
4. No pending template build, unowned guest, active coding task or ambiguous
   admission/recovery can be carried into retained-stop acceptance.

## Exact staging commands after readiness

Before final generation, root installs the reviewed candidate preflight/target
unit files and reloads the unit graph without starting the new target. The
generator requires both installed unit hashes; before this step only ownership
inventory is valid. No absent boot unit is silently omitted.

Root stages the committed `render_nested.py`, `ownership_plan.py` and
`lifecycle.py` in this **nested-worker** root0700 directory first:
`/root/cube-production/lifecycle-candidate-20260925`. These are concrete proposed
staging paths, not claims that the files are already installed. Commands below
only inspect the current worker and create new private files; an existing output
must never be overwritten to conceal a previous observation.

```sh
umask 077
python3 /root/cube-production/lifecycle-candidate-20260925/ownership_plan.py \
  --output /root/cube-production/lifecycle-candidate-20260925/ownership-final.json
python3 /root/cube-production/lifecycle-candidate-20260925/render_nested.py \
  --helper-source /root/cube-production/lifecycle-candidate-20260925/lifecycle.py \
  --output /root/cube-production/lifecycle-candidate-20260925/enrollment-final
```

Require zero remaining ownership changes. Review the private observed identities,
all hashes, actual plugin FDs, registry contract and every generated override.
The output deliberately has both review flags false and performs no install.
Use the separate installation checklist for root's explicit reviewed apply.
Recollect the final manifest after any input change; changing hold drop-ins,
unit ordering, dynamic quotas or a binary invalidates their prior hashes.
Preflight requires/starts Docker and is ordered after it; do not attach the Cube
preflight dependency to Docker itself, which would create a cycle. Pin the final
preflight/target/component drop-ins in the manifest as loaded configuration.

## Actual retained-stop acceptance, separate from host enrollment

After root installs the exact reviewed nested helper/config/overrides and
independently pauses the owned fixture with authoritative GET paused, save the
ordinary provider and owned-containerd no-task evidence. The following commands
run **inside the nested worker**, and the last one stops management components.
Root alone runs them in the already-approved empty/owned-fixture window:

```sh
umask 077
CUBE_REVIEW_MACHINE=$(cat /etc/machine-id)
CUBE_REVIEW_BOOT=$(cat /proc/sys/kernel/random/boot_id)
CUBE_REVIEW_DATA=$(findmnt -n -o UUID --target /data)
python3 /usr/local/libexec/baarcha-cube-worker-lifecycle.py verify-start \
  --machine-id "$CUBE_REVIEW_MACHINE" --boot-id "$CUBE_REVIEW_BOOT" \
  --data-uuid "$CUBE_REVIEW_DATA" > /root/cube-production/lifecycle-candidate-20260925/verify-before-stop.json
python3 /usr/local/libexec/baarcha-cube-worker-lifecycle.py shutdown-components \
  --machine-id "$CUBE_REVIEW_MACHINE" --boot-id "$CUBE_REVIEW_BOOT" \
  --data-uuid "$CUBE_REVIEW_DATA" > /root/cube-production/lifecycle-candidate-20260925/retained-stop.json
```

Do not invoke the last command while another actor can create/resume a guest.
It validates current identities, pins immutable container/PID generations,
stops management without force/remove, stops Docker last and syncs both disks.
Failure leaves shutdown incomplete; retain evidence and do not force a reboot.
An existing current-boot `/run/baarcha-cube-retained-stop.json` is an unfinished
attempt, not a file to delete to make a retry pass.

Root then uses its separately reviewed exact-QEMU QMP `system_powerdown` action,
observes the original process exit and boots the same disk pair under its hold.
No `quit`, SIGKILL or direct active-qcow copy is an accepted shutdown. Verify new
boot ID with unchanged machine/data UUID, persistent metadata, registry running,
exact retained guest identity and authenticated app/history/latest SQL after
ordinary resume. This manual acceptance does not generate a host supervisor
clean receipt and must not be relabeled as one.

## Remaining host and backup gates, in the shortest sound order

1. Finish manual retained-stop/reboot proof above. Then review the source-gate
   transition, actual lifetime-lock supervisor installation and empty initial
   enrollment. No adoption of the old live QEMU and no synthetic stopped-clean
   status. Complete the first real coordinator stop/boot/start reconciliation
   under a genuine controller/traffic drain. All hashes and receipts refer to
   that generation; the startup marker is cleared only by the reviewed command.
2. Prepare consistent recovery-role archives **before** the brief final pause
   window: controller key/config plus recoverable exact controller image/build,
   worker config/credentials and `seed.img`, exact launch/unit/helper inputs,
   platform PostgreSQL dump, library snapshots and retained Docker homes/history/
   migration-recovery journals. Close all outputs; final consistency requires
   writer fencing. A live tar of the 66 Docker homes is not an accepted backup.
3. With the worker truly off, controller stopped, both lifetime locks available
   and fresh real pause proof, execute the existing `cold_pair.py capture`.
   Its current space check reserves both virtual disk sizes plus artifacts; this
   is approximately 480GiB for the current 160+320GiB pair before extra roles.
   Root's RAID has room subject to authoritative free-space recheck. Capture,
   compare and hashing are measured downtime; do not promise a one-minute copy.
   The pause proof must be at most ten minutes old **when capture begins**.
4. Restart/reconcile the original from genuine receipts, keeping customer routing
   closed until acceptance. Seal the immutable captured copy with the existing
   verified public recipient, then upload the **full ciphertext** to the existing
   independent object-store prefix and stream-read it back to verify full bytes
   and SHA256. The prior 768-byte recipient test is not this full backup proof.
5. Decrypt using the independently held private key on the operator machine.
   Its limited free disk means stream the plaintext through SSH to a new private
   outer RAID `.partial` file if necessary. Do not parse or restore until the
   entire pipeline, especially GPG, exits zero and all transfer checks pass.
   Only then publish the closed plaintext tar and invoke `cold_pair.py restore`
   to a different fresh directory. A partial plaintext file is never evidence.
   No private key/passphrase is copied to the server.
6. Boot the restored **pair**, with separate QEMU identity/network/management
   addresses, no production routes/controller and no shared writable disks.
   Verify canonical bindings, scoped owner files/configs, actual completed task
   history/checkpoint replay and latest acknowledged app/PostgreSQL data against
   pre-capture markers. Use real synthetic owned app/history/SQL records for the
   rehearsal; empty inventory cannot prove those checks. Never set application,
   history or DB receipt flags based only on qemu-img/SQLite integrity.
7. Record off-host copy and application-restore receipts only after those actual
   checks; bind both to the exact current sidecar/ciphertext/evidence hashes.
   Configure and exercise monitoring with those real receipts. A newer backup
   currently requires its own restore receipt; a previous generation's restore
   cannot silently satisfy the monitor. Agree the operational cadence before
   activation: scheduled cold maintenance, maximum tolerated recovery-point
   interval, retention space and independently recoverable key custody.

A one-time backup does not finish the ongoing schedule. Cold backup cannot
protect writes acknowledged after its capture against losing the unmirrored
NVMe, nor replace the separate crash/latest-SQL recovery proof. There is no
implemented zero-downtime snapshot schedule or zero-data-loss replication to
claim. Keep these limits explicit while completing actual acceptance.
