# Closed backup-role preparation

`capture_roles.py` is a **candidate, not an executed full backup**. It never
stops or starts a source. Run its local checks with:

```sh
python3 -m unittest discover -s ops/cube/backup -p test_capture_roles.py -v
```

Fourteen focused tests pass, covering changed inventory, pending work, live
Docker writers, changed mounts/environment, required preview-key retention,
key-content drift, verified-TLS route checks, exact socket classification,
control-file warnings, partial-archive retention, recursive-output refusal, no overwrite, space reservation
and actual child descriptor inheritance. Docker/systemd/Caddy and GNU tar calls
are mocked in these tests. The independently executed PostgreSQL18 tool smoke
and earlier GPG/qcow transport tests remain separate evidence. This wrapper has
not run against customer data and is not a restore acceptance receipt.

## Inputs to prepare privately

Place `capture_roles.py` beside the reviewed `cold_pair.py`. Prepare a new
root0700 directory and a root0600 `roles-config.json`; do not put its contents in
Git or logs. The operator config has these fields:

| Field | Required value |
|---|---|
| `version` | `1` |
| `controller_id`, `controller_image` | Exact current full Docker ID/image digest |
| `controller_env_sha256` | SHA256 of canonical JSON of the actual inspect `Config.Env` array, without sorting the array |
| `inventory` | Exact result of `database_inventory()` against canonical SQLite read-only; includes all current Docker homes and snapshots, recovery path references and migration count |
| `docker_homes` | One entry per canonical Docker row: `sandbox_id`, full `container_id`, exact `image`, canonical host `source` for the sole `/home/sandbox` bind |
| `reviewed_caddy_sha256` | SHA256 of canonical JSON of the **actually loaded fenced** Caddy configuration |
| `preview_host` | Existing certificate-bearing `s-<id>-<port>.preview.65.108.225.153.sslip.io` hostname; no token or query |
| `stopped_writer_units` | Every reviewed direct-writer service/timer. At least project-env-apply, classroom-egress and fennec-meet-egress, both `.timer` and `.service`, are mandatory; add Motion's actual installed writer units when present |
| `reviewed_files` | Map of exact canonical root-owned configuration file paths to SHA256, including active Compose/override/env inputs, worker launch/manifest dependencies and `/var/lib/sandboxd/cube-preview-auth.env` |
| `role_paths` | Exactly `controller-config`, `worker-config`, `worker-launch`, `rollback-extra`, `library-extra`, each an explicit absolute-path list |
| `image_archive` | `path`, `sha256`, `image_ids` from the completed immutable export receipt; must cover all current source images and controller image |
| `ephemeral_sockets` | Explicit list of `path`, current `device`, `inode`, reviewed `reason`; normally empty. Only exact socket inodes can be excluded, never regular files or parent directories |
| `upload_storage_review` | `s3-only-confirmed` after actual provenance review, or `disk-roots-listed` with every disk upload root in `library-extra` |
| `role_byte_budget` | Positive integer maximum aggregate closed-role bytes |
| `minimum_free_bytes` | At least `568 * 2**30 + 2 * role_byte_budget`, allowing staged roles and the subsequent pair's role copy |
| `postgres` | `sandbox_id`, full `container_id`, canonical `data`, current `data_device`/`data_inode`, `major:18`, exact immutable image below, and the nonempty exact previously observed `socket_paths` (socket plus lock) |

The PostgreSQL image is
`docker.io/library/postgres@sha256:9e73daeb439141c2b11eea2463f5f1a3b269fd90d897b41cddb7cb440f21aa5d`.
It runs as UID1000, network-none, read-only, 1CPU/256MiB, with only the stopped
PG data directory mounted read-only. `PG_VERSION` must be18, `postmaster.pid`
and the observed socket/lock must have disappeared naturally. Exit0 with
**any stderr warning**, or control state other than `shut down`, refuses capture.
Do not remove source files to obtain a pass. If this fails, retain all WAL and
require the separate isolated-copy recovery/SQL proof.

The preview signing-key file and `/opt/baarcha/landing.env` must explicitly occur
in `controller-config`. Actual controller `SANDBOXD_ENV_FILE` must reference the
persistent signing-key file. Its content hash and all pinned config hashes are
rechecked between each archive. The wrapper includes the exact full private
container inspections/config alongside the image archive inside controller-config.
The encryption key itself remains the separate `controller-key` role.

`rollback-extra` must include all migration archive roots and every recorded
Cube recovery artifact. `rollback` automatically adds every complete owner home,
including `.git`, `.runtimed`, provider state, non-app files and database/WAL.
`library` includes every recorded snapshot path plus reviewed upload roots. No
home-manifest policy is applied to this encrypted operator recovery archive.
Do not extract owner-controlled tar links over a live host: restore roles only
into the independently reviewed isolated recovery environment.

## Exact phase boundary and commands

Refresh the private plan **after** the canonical fixture completes and after any
legitimate new project. `plan` validates schema/counts only; it does not assert
live eligibility, run probes, or establish a fence:

```sh
python3 /PRIVATE/STAGE/capture_roles.py plan --config /PRIVATE/STAGE/roles-config.json
```

Root then owns the actual scoped route/direct-writer drain, zero active coding
and thumbnail capture work, stopped canonical controller with restart policy
`no`, and graceful shutdown of every mapped Docker source. Review existing
connections and all direct writers before this phase. The script's GET503 probes
and exact Caddy hash confirm the reviewed fence is still loaded; they cannot
independently prove that every previously accepted stream stopped. No blanket
inference/voice shutdown is required by this tool.

While that fence remains in place, run on the native outer Linux host:

```sh
umask 077
nice -n 10 ionice -c2 -n7 python3 /PRIVATE/STAGE/capture_roles.py capture-frozen \
  --config /PRIVATE/STAGE/roles-config.json --output /PRIVATE/STAGE/closed-roles-01
```

It nonblockingly acquires the existing platform deploy, runtime deploy and outer
operator locks plus canonical SQLite's maintenance lock. If an orchestration
parent already holds the three operator locks, supply their exact ordered
inherited descriptors as `--inherited-lock-fds FD1,FD2,FD3` and pass them through
`subprocess` explicitly; no release/reacquire gap. The wrapper acquires SQLite's
lock itself before observation. Child archive/probe processes inherit the held
locks. Do not simultaneously hold a second exclusive SQLite descriptor in a
parent and expect this invocation to acquire it.

The wrapper refuses open SQLite users, any task/admission/recovery/migration in
progress, changed identities or extra owner mounts, running owner containers,
and any unrelated running container sharing a writable owner mount. It rechecks
these conditions between archives. Config paths must be canonical/root-owned;
owner homes may contain owner-owned files and preserved links. All special files
are refused except exact reviewed ephemeral sockets. Root-only external writers
remain an operator-controlled assumption, not an adversarial-host guarantee.

GNU tar has a15minute timeout per role and an enforced file-size ceiling; the
PostgreSQL custom dump/list phase is bounded to370seconds. All output is private.
An error leaves `.INCOMPLETE`/private logs and never publishes a success manifest;
never automatically resume a partially completed role set after writers restart.
The platform dump is a transaction-consistent PostgreSQL snapshot, not a global
simultaneous transaction with the frozen SQLite/homes. Connection URL options are
rejected for explicit review rather than silently discarded.

Only `complete.json` establishes closed role files. It explicitly leaves
`full_pair_captured` and `application_restore_verified` false. The preparatory
`controller-before-pause.sqlite` is diagnostic; **cold_pair still captures the
canonical SQLite database itself** at the actual paused worker checkpoint.

Next, root runs the real worker stop coordinator and uses its new matching
`pause-receipt` with these roles in `cold_pair.capture`. Restart the original
worker/Docker/controller/traffic after the cold disk copy and comparisons,
**before** encryption/upload/readback. Off-host transfer and full paired clone
app/history/SQL restoration still need the real sequence in
[PAIRED-RESTORE-SEQUENCE.md](PAIRED-RESTORE-SEQUENCE.md).
