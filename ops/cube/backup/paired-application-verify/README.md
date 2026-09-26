# Exact restored-canary verification (prepared, not executed)

`control-plane/cmd/cube-pair-verify` verifies the first full paired restore of
owned app `01M3CZB4HXT2Y8HP8CEY75PCWY`, sandbox
`01M3D1Q0E1KM1FEM244XVHEC65`, operator run `8bb03c01d5527213`.
It never boots, resumes, creates, imports, configures or deletes a guest. It never
starts a copied controller, connects to Docker, submits an AI task, changes a
SQL row or creates a screenshot. The only guest requests are authenticated GETs.

This source and its fixture tests are **not an actual full-pair restore pass**.
The operator-authored source fixture passed separately; the four failed AI tasks
remain failed. The verifier deliberately fails if those source expectations have
changed, or if another Cube binding has appeared in the captured canonical DB.

## Prerequisites before the independent restore window

1. Finish a genuine coordinated nonempty stop and cold paired capture, including
   closed controller SQLite/key, both worker disks, current configuration/launch
   roles, Docker homes/history/PostgreSQL, platform database, library and upload
   recovery authority. Preserve the operator fixture's entire private journal
   directory in the encrypted rollback role. Its final journal SHA is
   `8dcee6fde79ed46c4eeb527c342fe9598c2ee1ac2ed77a620ddce759d219e3b0`.
2. Restart the original worker/controller and restore normal traffic **before**
   encryption and offhost upload. Record the real cold capture time, not reseal
   time. Verify the complete encrypted download hash, independent local GPG
   exit0, and complete remote plaintext sink exit0 before parsing plaintext.
3. Run existing `cold_pair.py restore` against that independently decrypted
   archive into a new root0700 directory under
   `/opt/baarcha-cube/backup-generations/`. This verifies every exact member hash
   and both qcow2 integrity/compare contracts. Do not use an unverified decrypt
   pipeline or substitute the previous129MiB synthetic archive.
4. Extract the needed nested role files into that same private generation using
   the separately reviewed role extractor. Keep every original archive intact.
   Record the exact restored controller-key, operator journal, four task result
   receipts and four event receipts. Regular verifier inputs must be root0600,
   single-linked, canonical paths without symlink ancestors. The closed SQLite
   copy must have no WAL/SHM/journal and no process using it. Hash it against the
   strict restore manifest; never generate a replacement key.

Only then schedule a separate controlled clone window. The original and clone
40GiB workers cannot coexist safely on this host. The caller, not this verifier,
performs real traffic/task drain, clean original stop, clone boot and eventual
clone stop/original restart. It retains the four existing operation locks and an
**exclusive** `/opt/baarcha-cube/worker-01/backup.lock` for the entire clone window.
That fifth lock prevents the original supervisor from automatically starting.
Never release it while the clone is still running.

## Clone arrangement and verification inputs

Do not boot a copied production controller or give anything the production
Docker socket. The isolated clone uses only the restored pair, an independently
reviewed private management topology, no production routing/registration, and no
background job with authority over production. Restored credentials stay within
the private artifacts and authenticated exact-clone requests. The operator must
verify the clone's actual provider inventory and resume **only** the restored
canary before this command. No synthetic HTTP substitute is accepted as live
proof; unit-test fake responses test rejection behavior only.

This first verifier pins a separate operator SSH host-forward
`127.0.0.1:21222` on the clone QEMU and a dedicated SSH child exposing
`-L 127.0.0.1:21080:127.0.0.1:80 -p 21222 root@127.0.0.1`.
Use an exact reviewed command with strict host-key checking and no unrelated
forwards. The verifier checks both processes' root ownership, executable,
`/proc/PID/stat` start ticks and SHA256 of raw NUL-delimited command lines. The
clone QEMU must name both exact restored disk files, neither linked to an
original disk. Only `http://127.0.0.1:21080` is used for HTTP, with the restored
3031 supervisor or3000 web virtual host. No proxy environment or redirects are
used. The original worker unit must remain inactive and its QEMU absent.

Prepare `config.json` root0600 from the **closed, independently authenticated
source artifacts**, not newly invented expected data. Use `config.example.json`
as a schema example; its empty values intentionally cannot execute.

- `database`, `key`, `operator_journal` are absolute paths and SHA256 references
  inside the restored generation. The key is the original base64 master-key file.
- `runtime_id`, `template_id`, `domain`, `owner_token_sha256`, `config_revision`
  come from the reviewed source binding/owner captured with the pair. Require
  source preset `node-postgres`, owner `baarcha:103`, exact external project and
  applied configuration revision. Never disclose the owner token or key.
- `files` contains exactly `sandbox.yaml`, `server.mjs`, `public/index.html`,
  `.operator-recovery/8bb03c01d5527213/home-worker.mjs`, and
  `.operator-recovery/8bb03c01d5527213/app marker #.txt`. Use original manifest
  hash from `journal.before['sandbox.yaml'].sha256`; other hashes come from
  `journal.done['write:'+path].sha256`. The verifier cross-checks these entries.
- `tasks` references the four `failed-task-ID.json` and `.events` receipts from
  the verified operator journal directory. IDs must match exactly. The command
  compares copied SQLite results with those source receipts, then actual restored
  guest results and complete event replay, including terminal done and checkpoint
  IDs. SSE source timestamps are absent by design; event IDs/types/data are exact.
- `clone_root`/`clone_data` are restored disposable boot copies beneath the same
  generation, not the retained immutable originals. Each clone disk is a distinct
  single-linked regular file. Record clone/proxy process pins after controlled boot.
- `lock_fds` maps the four fixed operation-lock paths to inherited FD numbers;
  `worker_lock_fd` identifies the additional exclusive lifetime-lock FD. The
  coordinator must pass the same OFDs with `pass_fds`, hold them across failure,
  and never infer permission to restore traffic from this verifier alone.

Run in the caller's inherited-lock context:

```text
/root-private-reviewed-build/cube-pair-verify /root-private/config.json
```

The120s overall bound and15s request bound are finite. Source receipts and
responses are bounded; no body, prompt, credential, SQL content or URL token is
printed. Exit0 emits a small receipt bound to config/database/operator-journal
hashes and clone identity. Redirect stdout to a new root0600 evidence file, retain
stderr/exit status privately, and verify those hashes before claiming a pass.
No retry or cleanup is automatic. A refusal leaves the caller's locks intact.

## Exact proof and remaining scope

Success means the original manifest/source/app marker hashes match, all declared
processes are running, the live filesystem proof matches owner UID1000,0640/0750
modes, special filename, safe relative symlink and exact two-link inode identity,
and actual app health/visible marker/PG-backed acknowledged note row match. All
four failed task results and full replayed event streams survive.

Checkpoint IDs are compared in results/events; `.git` is intentionally excluded
from the existing file API, so this command does **not** prove complete Git
object closure or execute revert. It also does not restore the platform database
or re-run private image ACL checks, prove AI coding quality, or replace encrypted
archive integrity checks. Those are distinct acceptance records. The saved
private capture's bytes and platform upload/owner/provenance rows must be restored
and verified in an isolated platform fixture before claiming that broader result.

No Caddy modification is required by this verifier. The missing redundant active
Motion `@id` route is not a functional startup gap: the disk wildcard already
uses the same upstream and TLS policy.
