# VPS source backups

`backup.py` captures the controller database and decryption key, selects only
current VPS bindings, and asks the local worker to clone their writable disks.
It does not wake sandboxes or contact the B200. The exporter uses a networkless
libguestfs VM with read-only inputs and never runs tenant code. Existing operator
locks protect capture and serialize the export against bulk restores. The
recovery VM uses one CPU and 1 GiB of memory.

The saved scope is merged `/home/sandbox`, including source, Git, lockfiles,
private configuration, uploads, app data, and guest task history. Installed
`node_modules`, recognized virtual environments, and the exact reproducible cache
paths recorded in each recipe are excluded. Possible locally modified npm files
are retained separately using an installation-time heuristic, not registry
content comparison. Unknown binaries and custom cache locations remain included.

The controller key and app credentials make the entire generation private.
Directories are root-only. Never put generated artifacts or private receipts in
Git. Database files inside the home archive are crash-consistent filesystem
copies, not logical database exports. Platform database backup is separate.

Successful generations live in `/var/backups/baarcha-vps-source/TIMESTAMP`.
`VERIFIED.json` is written only after archive hash, size, gzip, tar, controller
integrity, identity, and dependency-exclusion checks pass. Only then are that
job's temporary worker clones removed. Failed clones remain available for
diagnosis. Seven-day retention applies to verified generations, keeping at
least two. The daily timer runs at 04:15 UTC with up to 20 minutes of jitter.

Recovery uses the saved image/runtime recipe and the private source recovery
pipeline in `../vps-capacity-20261008`. Reinstall from saved lockfiles, review
dependency exceptions, validate app data, test HTTP readiness and stop/wake, and
commit a routing change through the controller. Do not extract tenant archives
onto the host root or replace a live controller database for one app recovery.

This backs up current VPS bindings. It does not refresh unavailable B200 sources;
the separate October 8 emergency generation retains those originals. VPS-local
backups protect against sandbox loss, not loss of the VPS itself.

Install `backup.py` in `/usr/local/libexec/baarcha-vps-source-backup` on the host,
and `capture.py` plus `export-source.py` in `/root/baarcha-source-backup-tools` on
the local worker. The worker requires the existing libguestfs/kernel setup at
`/data/cube-source-backup-tools/kernel-env.json`. Verify one full generation before
enabling the provided systemd timer. Check `latest.json`, service exit status,
and any `FAILED.json` rather than inferring success from a timer invocation.

An interrupted export with a completed capture can be resumed using
`backup.py --resume TIMESTAMP`. It verifies the saved scope, reuses only finished
per-sandbox exports, and verifies all destination archives again. Do not resume
a failed or incomplete capture; investigate it and create a fresh generation.

The source exporter also excludes PNPM's generated stores and cache:
`~/.local/share/pnpm/store`, `~/.pnpm-store`, and `~/.cache/pnpm`. It retains
PNPM configuration/state, local dependency source, authored patches, and lockfiles.
`test_cache_exclusions.py` exercises the actual GNU tar patterns against retained
source/configuration fixtures; it passed on the VPS before deployment.

Archive downloads are paced by rsync at10MiB/s before data enters the host page
cache. Kernel writeback remains unthrottled so backup traffic cannot accumulate
a large throttled write backlog behind control-plane metadata commits.
