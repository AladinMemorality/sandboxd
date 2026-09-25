# Stage exact nested manifests and stop overrides — no installation

`render_nested.py` performs read-only collection on the actual nested worker and
writes a **new private staging directory only**. It never installs a unit, starts
or stops anything, changes an allow-file, or enables the host source gate.

Before running it, root must bring the reviewed management stack up under its
controlled maintenance procedure. Five native services must have actual active
MainPIDs: Cubelet is Type=forking, and Master/API/Ops/templatecenter Type=simple.
The collector resolves `/proc/PID/exe`, checks the exact installed path and stable
PID generation, and hashes the root-owned executable. A bash wrapper MainPID,
wrong basename, changed process, missing component or unknown layout fails. It
does not guess the real child behind a shell or widen the identity allowlist.

Inputs are only the root-owned reviewed helper source and a fresh output path:

```sh
python3 /PRIVATE-REVIEW-STAGE/render_nested.py \
  --helper-source /PRIVATE-REVIEW-STAGE/lifecycle.py \
  --output /PRIVATE-ROOT0700-PARENT/new-nested-lifecycle-stage
```

The placeholders above are deliberately not executable deployment paths. Stage
only reviewed source; this tool reads the helper to hash it and never imports or
executes a caller-supplied helper. Its fixed actual worker paths are:

| Role | Executable |
|---|---|
| Cubelet | `/usr/local/services/cubetoolbox/Cubelet/bin/cubelet` |
| Master | `/usr/local/services/cubetoolbox/CubeMaster/bin/cubemaster` |
| API | `/usr/local/services/cubetoolbox/CubeAPI/bin/cube-api` |
| Ops | `/usr/local/services/cubetoolbox/CubeOps/bin/cubeops` |
| Templates | `/usr/local/services/cubetoolbox/CubeTemplateCenter/bin/templatecenter` |

The collector verifies all eleven exact persistent plugin paths from installed
Cubelet TOML on XFS and containerd `no_sync=false`. It also verifies retained
MySQL/Redis/MinIO/registry data mounts on one of the two persistent filesystems.
It hashes launch/config/unit/drop-in files without copying their contents or
printing credentials. Vendor Docker fragment identities go to private evidence;
reviewed `/etc` overrides are pinned in the manifest. Existing boot-hold drop-ins
remain in the recorded artifact set and are never removed by the output plan.

Output files are root0600 inside root0700 directories:

- `observed-private.json`: actual worker/process/unit/mount evidence; keep private.
- `nested-lifecycle.json`: exact hashes and paths, with both review flags **false**.
- `overrides/*.conf`: sixteen fixed component helpers and one Docker service
  override, all with restart/escalation disabled and infinite graceful stop wait.
- `install-plan.json`: destination paths, content hashes and modes for root review;
  it is data, not an installer. It contains no shell hooks.

Root must compare against its actual accepted binary build and metadata evidence,
review the staged helper hash and every override, then explicitly approve private
manifest review flags. Root's application procedure must copy the **exact hashed
helper** to `/usr/local/libexec/baarcha-cube-worker-lifecycle.py` and install only
approved staged files. This generator provides no such mutation command. Runtime
preflight admits this one exact helper artifact path; it does not admit arbitrary
`/usr/local/libexec` programs from a manifest.

After any reviewed apply, root must inspect loaded ExecStop argv and properties,
not just files: the candidate refuses old Compose helpers, finite stop timeouts,
restart-on-failure, or ignored ExecStop errors. Actual native MainPID handoffs and
retained stop/clean reboot still require functional acceptance. The optional
s3lvol unit must remain inactive; web UI remains optional for runtime readiness.
The host's `STOP_COORDINATOR_IMPLEMENTED` source gate remains false until separate
review. Neither generated flags nor a staged install plan can override it.

## Retained registry restart contract

The registry has no systemd component. Before collecting the final manifest, root
must review its existing full container ID, image digest, loopback-only
`127.0.0.1:5000` binding and retained `/var/lib/registry` mount, then explicitly
change **that exact existing ID** to `docker update --restart=always FULL_ID`.
This is a reviewed installation action, not performed by the collector or helper.
No container is created, replaced or removed. The generator rejects
`unless-stopped`; the complete nonsecret definition is pinned in the manifest.

Docker documents that an explicitly stopped `always` container starts at the next
daemon boot; `unless-stopped` remains stopped. See [Docker restart policy
semantics](https://docs.docker.com/engine/containers/start-containers-automatically/).
Management's Docker service/socket boot hold remains the enclosing fence. The
registry's graceful stop still targets its frozen full ID, waits without a kill
timeout, retains it, and stops Docker last. It cannot restart itself after a
manual stop until daemon restart. Readiness subsequently requires the exact
container running without OOM, unchanged definition and a bounded local
`GET /v2/` response from the registry. There is no second process manager.

Actual acceptance must verify this across the planned worker/Docker restart;
mocked policy/readiness tests do not prove a real daemon reboot. At least ten
seconds of successful initial registry runtime is required for Docker to monitor
its restart policy. This check does not authorize lifting the boot hold or the
host source gate.

## Ownership-only preparation under the existing boot hold

Some stock toolbox binaries/scripts arrive owned by tar UID 1001. Before starting
native processes for collection, root can run `ownership_plan.py --output
/PRIVATE-ROOT0700-PARENT/control-ownership-plan.json`. This is read-only: it records
exact paths, device/inode, mode, current ownership and content SHA256. It includes
the five fixed binaries, launch/config scripts, loaded `/etc` units/drop-ins and
their control directories. It never recursively traverses owner homes, template
images, registry volumes, `/data`, or customer files. Vendor units are checked but
not included for mutation. Symlinks, hardlinked files, special files and
writable-by-group/others inputs fail instead of broadening the change.

Root must review the private plan while all management remains held. Apply only
entries marked `ownership_change_required`, parent directories first: reopen
with O_NOFOLLOW, require exact recorded device/inode/mode/UID/GID and rehash file
bytes, then `fchown(fd, 0, 0)` and fsync. Recheck unchanged bytes, inode and mode
and root ownership afterward. Do not use recursive chown, replace files or change
modes. The plan deliberately contains no executable application hook; root owns
that bounded reviewed operation. Re-run the collector and require zero ownership
changes before native startup, then collect actual MainPID/executable evidence.
Retain both plans privately with application proof. Parent-directory ownership
is included because otherwise a non-root directory owner could replace a pinned
root-owned control file.
