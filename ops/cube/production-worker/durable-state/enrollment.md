# Empty-worker enrollment review — not executed

This applies only to the dedicated `baarcha-cube-worker-01`, upstream
`31d911e430fdf8a8879bd062b8e78066c1a8e89d`, after independent zero all-state
inventory/zero guest-task proof. Production Docker service and controller remain
running. A zero Cube inventory does not establish absence of retained disks.

## Observed storage and namespace

Read-only inspection on2026-09-25 found the following:

| Location | Actual use | Enrollment handling |
| --- | --- | --- |
| `/data/cubelet/state` in Cubelet's pinned private mount namespace | tmpfs; old critical DB shards and runtime mounts | Escrow after writers stop, before the namespace disappears. |
| `/data/cubelet/state` in initial worker namespace | XFS underneath; complete walk found one directory, zero DBs and zero symlinks | Recheck after reboot; do not delete files to pass the startup guard. |
| `/data/cubelet/cleanup/db/{b..k}/meta.db` | Persistent XFS cleanup bookkeeping | Preserve untouched; new cleanup root is distinct. |
| `/data/cubelet/persistent-metadata` | Proposed new XFS metadata root | Must be explicitly created root-owned0700; render all11 exact plugin overrides. |
| `/data/cubelet/storage` | Existing current disks/native CoW objects and catalogs | Keep all paths and contents, including quarantined old objects. |
| `/data/cubelet/state/io.containerd.mount-manager.v1.bolt/mounts.db` | Kernel mount-manager state and transient target directory | Escrow old state. Not one of the11 relocated metadata roots; remains under volatile State. |
| `/run/cubelock.db` | Process locking state | Remains transient; not project durability storage. |

Cubelet's namespace pin is
`/usr/local/services/cubetoolbox/cubeletmnt/mnt`.
A process restart can reuse this existing namespace. An OS reboot removes its
tmpfs/pin mount; a service restart alone is not equivalent. The configured State
path's XFS appearance from an ordinary SSH shell is not proof of metadata durability.
Mount-manager registration explicitly selects PropertyStateDir in
`Cubelet/plugins/mount/manager.go`; persisting stale kernel mount targets was not
added to this patch. Its use must still be exercised by the restart acceptance.

The live config is
`/usr/local/services/cubetoolbox/Cubelet/config/config.toml` and the binary is
`/usr/local/services/cubetoolbox/Cubelet/bin/cubelet`.
The pre-enrollment installed binary is
`254b5e7b11c7c6f865e3e1816c3737a4041424b7e84b5aba7dc406e596eecbed`.
The candidate is `/root/cube-production/durable-native-candidate/cubelet-candidate`,
SHA256 `a61a43c531b8e7854dd8ee064db0d1e160d42fcb4d50e7c4b3c98d447e63e75a`.

## Retained original object and startup behavior

The failed running fixture `1a1474444e064d6f8da340f324f4fd7f` is absent from
Master but its current object remains at:

```
/data/cubelet/storage/xfs/objects/volumes/tpl-tpl-ce1ee426e686460bbc8c3bfc-build-rootfs/sb-1a1474444e064d6f8da340f324f4fd7f-rootfs-gen0
```

Read-only stat recorded size10737418240, inode270239769, device64784,
105184 allocated512-byte blocks, mode0644, mtime_ns1790296419384786773.
These are observations, not substitutes for a fresh exact identity comparison.
Preserve its parent/template objects and the independently captured bundle at
outer `/opt/baarcha-bench/cube-current-r4-20260925/input`.
Captured current-image SHA256 is
`4e83376fca1c66a1e9da4b169e1523ddbcbcc8efb6a88cd56a9335edb08c3ba9`;
PMEM lower SHA256 is
`ba20ac338f733fc2cc81bd24eb98385cda9c5d8e88376f3769fdc8096020861a`.
Do not chmod, rename or reset the retained native object as a quarantine action.
Quarantine here means no reuse/deletion and an explicit manifest/exclusion.

Normal plugin startup in `storage/plugin.go` calls `local.init`, which initializes
the configured DB/native engine and `RecoverStorageState`. That recovery reads
DB entries plus the exact failover directory; it does not traverse all native
`sb-*` volumes to delete unreferenced files. Patch0005 keeps records whose
referenced object is missing instead of deleting the whole record. With a new
empty metadata root there are no old records to recover; this is explicit empty
node enrollment, not automatic recreation of lost identity.
`storage/reconcile.go` has no orphan-volume sweep. The images volume-lifetime
scan targets configured `/data/cubelet/root/volume`, not the native objects path.

**Never invoke `InitHost`, `cubecli unsafe init`, or any reset-node operation.**
`services/nbi/service.go` exposes `InitHost` through gRPC and calls `engine.Init`.
`storage/local.go`'s separate exported `Init` resets native CoW state and removes
storage directories. It is not the normal startup `local.init` path. The pinned
source search found no Master call to InitHost; its CLI is explicitly unsafe.
The native cubecow library is prebuilt and its internal startup behavior has not
been fully audited. Independent cold disk-pair escrow is therefore required;
this source trace is not a universal no-GC guarantee.

## Reviewed operator sequence

1. Keep Cube customer routing/admission off. Verify exact worker hostname,
   machine-id, data UUID `793c3349-db9c-4815-9842-989ed484f1f8`, zero owned/build
   jobs, zero all-state inventory, no guest tasks and retained-disk identities.
   Do not stop production Docker/control services.
2. Hold fresh-worker lifecycle controllers and future autostart. Stop its
   management writers and Cubelet using the reviewed graceful-stop override
   below; never run an Init/reset API. Verify no process still has a
   metadata DB or current guest disk open for writing and no VMM/task remains.
3. While the namespace pin still exists, enter it read-only for metadata escrow.
   Example inspection: `nsenter --mount=/usr/local/services/cubetoolbox/cubeletmnt/mnt findmnt -T /data/cubelet/state`.
   Archive the DB-containing State directories, persistent cleanup tree, old
   config/binary and native catalog/object manifests into private0700 staging,
   with0600 archives and hashes. A copy made before writer stop is only a
   forensic hot copy, never a consistent DB backup. Do not archive transient
   sockets as recoverable database state. If the namespace has disappeared,
   stop and assess the loss rather than fabricate a clean escrow.
4. Transfer the private escrow to the authorized outer private directory. Hold
   Cube component autostart across the next boot using reviewed persistent
   unit holds, and record precisely those changes. Do not rely on a runtime
   systemd mask, which disappears at reboot.
5. Gracefully power down this explicitly empty worker through QMP
   `/opt/baarcha-cube/worker-01/qmp.sock` (`system_powerdown`) and wait for actual
   QEMU exit. The observed outer unit has no ExecStop; plain systemctl stop
   must not be assumed to deliver ACPI shutdown. Never force-kill for checkpoint.
6. With QEMU stopped, independently checkpoint both
   `/opt/baarcha-cube/worker-01/root.qcow2` and
   `/mnt/nvme/baarcha-cube/worker-01/data.qcow2`, plus seed/unit/QEMU identities,
   to the reviewed private destination. Use an independent cold copy or
   standalone conversion, verify qcow2 backing-chain identities and source/dest
   hashes or image comparison, and fsync files/directories. Do not claim a hot
   qcow2 copy is consistent. This empty test-worker checkpoint is not an ongoing
   customer backup policy.
7. Boot with Cube components held. Verify data UUID/XFS, no unexpected legacy
   initial-State DBs and retained-object identities. Create the new root with
   root ownership0700 and fsync it/its parent. Use
   `python3 render_config.py OLD_PRIVATE_CONFIG NEW_PRIVATE_CONFIG`; review
   the parsed field changes without printing secrets. Preserve old config and
   install the exact candidate and new config only under the component hold.
   No old metadata is silently copied into the new roots.
8. Release only reviewed fresh-worker controls. Inspect actual open critical DB
   paths inside its namespace: all11 overridden roots must be on persistent
   XFS; containerd no_sync must be false. Verify all expected templates and
   zero guest inventory, protected BPF identity and original retained disk.
   If any guard fails, stop; do not delete legacy state to bypass it.
9. Execute owned acknowledged file/SQL and pause/resume/clean-reboot tests,
   then an explicitly coordinated acknowledged-pause power-loss test. Verify
   metadata/resource labels, current disk and latest SQL, not just API health.
   A previously running guest's native same-ID cold restart remains a separate
   capability; current-disk replacement proof must not be renamed a native pass.

Rollback first fences admission and preserves any newly written metadata/data.
Before new guest writes, the paired cold checkpoint can restore the exact prior
empty-node state. After new required writes, neither old volatile config nor an
old paired checkpoint alone is a valid rollback: preserve/recover the latest
current disk and durable journal first. Retain the old failed fixture escrow
throughout. No step above was executed by this review.

## Command checklist for the coordinating operator

These commands are preparation instructions, not an unattended migration script.
Run only in the verified worker after the independent empty-node checks above.
Keep each phase's checks and private outputs before moving on. Commands which
stop services or copy disks were not executed by the patch author.

The observed enabled start sources are `cube-sandbox-control.target` and
`cube-sandbox-cube-templatecenter.service`; the compute target is disabled.
Disabled individual units are still started by the control target's Wants.
A persistent file condition on every installed Cube service/target holds both
paths. Use a new private escrow directory and refuse existing hold overrides:

```bash
set -euo pipefail
umask 077
test "$(hostname)" = baarcha-cube-worker-01
ENROLL=/root/cube-production/durable-enrollment-20260925
mkdir -m 700 "$ENROLL"
test "$(findmnt -n -o UUID -T /data)" = 793c3349-db9c-4815-9842-989ed484f1f8
systemctl list-unit-files --no-legend 'cube-sandbox-*.service' 'cube-sandbox-*.target' > "$ENROLL/units-before.txt"
mapfile -t CUBE_UNITS < <(awk '{print $1}' "$ENROLL/units-before.txt")
for unit in "${CUBE_UNITS[@]}"; do
  test ! -e "/etc/systemd/system/$unit.d/99-durable-enrollment-hold.conf"
done
install -m 600 /dev/null "$ENROLL/HOLD"
for unit in "${CUBE_UNITS[@]}"; do
  install -d -m 755 "/etc/systemd/system/$unit.d"
  printf '[Unit]\nConditionPathExists=!%s/HOLD\n' "$ENROLL" > "/etc/systemd/system/$unit.d/99-durable-enrollment-hold.conf"
done
systemctl daemon-reload
```

The stock Cubelet ExecStop is **not** guaranteed graceful: it sends TERM, waits20s,
then sends SIGKILL. Override that behavior before stopping this empty worker:

```bash
STOP_OVERRIDE=/etc/systemd/system/cube-sandbox-cubelet.service.d/98-durable-enrollment-stop.conf
test ! -e "$STOP_OVERRIDE"
cat > "$STOP_OVERRIDE" <<'UNIT'
[Service]
ExecStop=
ExecStop=/bin/kill -TERM $MAINPID
Restart=no
SendSIGKILL=no
TimeoutStopSec=120s
UNIT
systemctl daemon-reload
systemctl stop cube-sandbox-cube-lifecycle-manager.service cube-sandbox-cube-api.service cube-sandbox-cube-proxy.service cube-sandbox-cube-templatecenter.service cube-sandbox-cubeops.service cube-sandbox-webui.service
systemctl stop cube-sandbox-cubemaster.service
systemctl stop cube-sandbox-cubelet.service
test "$(systemctl show cube-sandbox-cubelet.service -p MainPID --value)" = 0
# Review exit/result, not only a successful `systemctl stop` exit code:
systemctl show cube-sandbox-cubelet.service -p Result -p ExecMainCode -p ExecMainStatus > "$ENROLL/cubelet-stop-result.txt"
```

If Cubelet remains alive or stop times out, stop this procedure and inspect; do
not force-kill or proceed to a claimed clean backup. Verify no other guest/VMM,
DB writer or current-disk file descriptor exists before escrow. LCM/proxy stop
scripts remove their own control containers; they do not call InitHost. Do not
include raw process environments or private configs in terminal output.

This namespace archive includes regular files, directories and symlinks, but
excludes ephemeral sockets/devices. It does not follow symlinks or nested mounts.
Run only after all metadata writers are confirmed stopped:

```bash
NS=/usr/local/services/cubetoolbox/cubeletmnt/mnt
findmnt -n -T "$NS" -o FSTYPE | grep -qx nsfs
nsenter --mount="$NS" findmnt -T /data/cubelet/state > "$ENROLL/state-mount.txt"
nsenter --mount="$NS" sh -c 'cd /data/cubelet/state && find . -xdev \( -type d -o -type f -o -type l \) -print0' > "$ENROLL/state-filelist.nul"
nsenter --mount="$NS" tar --numeric-owner --xattrs --acls --null --no-recursion -C /data/cubelet/state -T "$ENROLL/state-filelist.nul" -cpf "$ENROLL/legacy-state.tar"
tar --numeric-owner --xattrs --acls -C /data/cubelet -cpf "$ENROLL/legacy-cleanup.tar" cleanup
install -m 600 /usr/local/services/cubetoolbox/Cubelet/config/config.toml "$ENROLL/config-before.toml"
cp --preserve=mode,timestamps /usr/local/services/cubetoolbox/Cubelet/bin/cubelet "$ENROLL/cubelet-before"
chmod 600 "$ENROLL/cubelet-before" "$ENROLL/legacy-state.tar" "$ENROLL/legacy-cleanup.tar"
sha256sum "$ENROLL/legacy-state.tar" "$ENROLL/legacy-cleanup.tar" "$ENROLL/config-before.toml" "$ENROLL/cubelet-before" > "$ENROLL/escrow.sha256"
sync -f "$ENROLL"
```

Verify the pinned namespace sees the expected old DB shards before accepting the
archive. Preserve the original archive even if later analysis finds it was only
forensic. No archive should be restored blindly into the new11 plugin roots.
Stop fresh-worker remaining controls/dependency databases gracefully before
QMP powerdown. The hold itself persists through reboot and is included in the
cold checkpoint; record it for rollback operators.

After the cold checkpoint and held reboot, creation/rendering is explicit:

```bash
test ! -e /data/cubelet/persistent-metadata
install -d -o root -g root -m 700 /data/cubelet/persistent-metadata
sync -f /data/cubelet/persistent-metadata
python3 /REVIEWED/STAGED/render_config.py "$ENROLL/config-before.toml" "$ENROLL/config-durable.toml"
sha256sum /root/cube-production/durable-native-candidate/cubelet-candidate
# Compare the exact a61a43... hash above before installing.
install -m 755 /root/cube-production/durable-native-candidate/cubelet-candidate /usr/local/services/cubetoolbox/Cubelet/bin/cubelet
install -m 600 "$ENROLL/config-durable.toml" /usr/local/services/cubetoolbox/Cubelet/config/config.toml
sync -f /usr/local/services/cubetoolbox/Cubelet
```

Replace the reviewed staged renderer path explicitly; no path is discovered from
a guest. Review initial-State emptiness and installed hashes before releasing the
HOLD. Keep its override definitions and the graceful-stop override documented;
release only the hold file under the coordinating operator, then start reviewed
fresh-worker units in dependency order. No blanket unmask/enable or automatic
production routing belongs to this enrollment procedure.

## Tested helper commands

`post_boot.py` is read-only; `install_after_boot.sh` installs only after its
preflight and never starts services or releases the hold. Stage it together
with `render_config.py`, `post_boot.py` and their reviewed hashes. Readiness
uses the committed eight-template manifest `templates-2026-09-24.json`.

```bash
# Inside the held, newly booted verified worker. Substitute exact escrow paths
# and the machine ID/previous boot ID from the coordinator's private receipt.
bash install_after_boot.sh OLD_PRIVATE_CONFIG EXPECTED_MACHINE_ID PREVIOUS_BOOT_ID NEW_PRIVATE_INSTALL_RECORD_DIR
# After separate review/start by the coordinator:
python3 post_boot.py ready --old-config OLD_PRIVATE_CONFIG --machine-id EXPECTED_MACHINE_ID --previous-boot-id PREVIOUS_BOOT_ID --templates REVIEWED_EIGHT_TEMPLATE_MANIFEST
```

The helper preserves original config/binary, records unchanged legacy cleanup DB
hashes and atomically installs/fsyncs the exact candidate/config. Partial failure
leaves services held for explicit operator review; it does not roll back newly
written state automatically. Readiness checks actual process executable and open
DB paths, not merely config text. All10 database roots must have actual open DBs
and XFS in Cubelet's namespace. The11th root, netfile, contains lazy per-guest
files, not a database; an empty node may not have created it, so its reviewed
persistent parent filesystem is reported separately. Volatile mount-manager DB
is explicitly permitted; any other open State DB fails readiness.

Startup script review is a separate prerequisite: `cubelet-start.sh` invokes
`prepare-compute-role.sh` and `write_cubelet_s3lvol_enable`. The observed latter
can edit configuration. Read and hash the exact installed scripts before release;
verify the preparation script does not replace metadata/data paths. Readiness
compares parsed live config to the exact rendered config, catching unexpected
startup rewrites, but detecting a rewrite after startup is not a substitute for
reviewing a destructive script before execution. Never run installer/config
regeneration scripts to resolve a missing DB/template automatically.
