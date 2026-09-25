# Whole-home archive validation and owned-fixture conversion

These tools validate an **uncompressed merged `/home/sandbox` tar**, not a
runtimed workspace ZIP or home-v2 ZIP. They never extract tenant paths on the
controller. Run filesystem replay/export only inside the dedicated rescue VM;
use `rescue_export.py` for that separate stage.

`archive_validate.py` parses bounded physical tar headers and metadata, then
checks paths, ownership, modes, link resolution, entry/payload limits and archive
termination. It rejects devices, FIFO/socket nodes, sparse records, unsupported
xattrs/ACL metadata and escaping links. A reviewed external symlink plan must
bind every literal path/target to the complete archive SHA and destination image
SHA; this is validation data, not permission to follow those links. Archive
hardlinks must resolve entirely inside home. The exporter additionally checks
inode link counts, which a tar alone cannot prove.

`archive_convert.py` supports the **owned PostgreSQL recovery fixture** only.
It separates `workspace/app` into `app.zip`, preserves `.bashrc`, `.bash_logout`,
`.profile`, `.cache`, `.baarcha-postgres` and `.cube-crash-fixture` in `home.zip`,
and emits the exact home-v2 manifest. Hardlinks expand only from verified bounded
regular members; modes and literal internal symlinks survive. Original PGDATA,
WAL and `postmaster.pid` remain intact. No stale PID cleanup occurs here.

```sh
python3 archive_validate.py --help
python3 archive_convert.py home.tar NEW_OUTPUT_DIRECTORY \
  --expected-marker latest.private.json \
  --go-validator /reviewed/path/recovery-archive-check
```

Build `archive_check.go` inside a temporary command directory in the real
control-plane module. The checker calls `PrivateWorkspaceFileDigest` and
`PrivateHomeDigest`, so conversion succeeds only after the actual existing
Go archive contracts accept both outputs. Output directories must be new;
archives/manifests are private and flushed to disk.

Conversion does **not** restore `.runtimed` credentials/configuration or convert
user task artifacts/history. Fleet recovery requires separate canonical task
history import, reviewed runtime configuration/template binding, and the exact
approved exceptional-symlink contracts. The fixed fixture converter fails on
unreviewed home scopes; it must not be silently expanded or presented as full
fleet recovery. PG structural layout and matching JSON markers do not establish
PostgreSQL recovery: the replacement must boot and return the committed SQL
marker through the authenticated fixture API.

## Native Linux overlay evidence (2026-09-25)

The once-only `overlay_fixture.py` run passed in `baarcha-cube-rescue`, an isolated
QEMU/KVM VM, with a new owned 128 MiB ext4 file and two trusted synthetic layers.
Its nonloopback NIC was down during the run and restored afterward. The systemd
job was bounded to two CPUs, 3 GiB memory and 300 seconds, with a 240-second child
timeout. No customer disk, worker mount or VM power operation was involved.

The fixture exercised actual ext4/overlay mounts and the unmodified exporter:

- File overwrite and whiteout deletion, including delete/recreate directory.
- Opaque directory in a trusted lower layer. The negative control proved that
  GNU tar extraction with `--xattrs` alone omits `trusted.overlay.opaque`; the
  actual exporter with `--xattrs-include=*` retained the correct visible tree.
- Hardlink relationship, literal symlink and numeric permissions.
- Latest markers written after an independent older backup; the older image
  retained baseline markers and an unchanged hash.
- Bounded whole-home validation, conversion and actual Go ZIP import contracts.

Result: [`results/2026-09-25/overlay-result.json`](results/2026-09-25/overlay-result.json).
The job exited zero, all fixture mounts were independently absent afterward,
and the NIC was up. Synthetic input images and reports remain private in
`/var/lib/cube-rescue/overlay-fixture-20260925-01` for review. No long-lived
fixture process remains. The PG bytes in this fixture are deliberately fake:
`postgres_crash_recovery_proven`, `replacement_verified` and
`production_accepted` remain false.

Offline checks: `python3 -m unittest discover -s
ops/cube/production-worker/recovery -p '*test.py'` passed 25 tests at this point,
including both possible hardlink tar orders and negative overlay assertions.
The actual native fixture is not run by these unit tests.
