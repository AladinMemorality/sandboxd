# Owner workspace transfer and dependency preparation

The private migration API is distinct from publication. Public/remix ZIPs still
use `SanitizeSourceArchive`: they exclude `.git`, owner data, credentials,
`node_modules`, `.venv`, and runtime state. An existing owner's migration uses a
private archive, which deliberately includes that owner's app-root data and Git
history. Never use private archives as published artifacts or across owners.

## Private transport

The authenticated guest API exposes `POST /workspace/quiesce`,
`GET /export/private-workspace`, `PUT /import/private-workspace`, and
`POST /workspace/resume`. The control plane must additionally enforce the owner,
write fence, migration journal, and provider cutover transaction.

Quiescence requires UID1000 and the Cube bootstrap's `RUNTIMED_CUBE_GUEST=1`
marker. It blocks in-flight/future guest API writes, rejects active tasks, suspends supervised process restarts, and stops detached
same-UID processes with pidfds. It excludes runtimed and its bootstrap ancestor
chain. If processes cannot be inspected/stopped, export/import fails closed.
The fsynced fence marker survives supervisor reexec; config may be applied while
quiesced, and an explicit resume is required before application processes run.

Private ZIP validation rejects traversal, duplicates, special files, absolute
or escaping symlinks, and entries beneath symlinks or regular-file ancestors.
Relative links contained in the app root are preserved. The only absolute-link
exception is a `.venv/bin/python*` executable leaf pointing exactly to
`/usr/bin/python3` or `/usr/bin/python3.N`; the destination guest must have that
executable. The reviewed source/destination image must still match Python ABI
for native wheels. Absolute directory links and entries below links remain
rejected. This exception never applies to source publication. Directory-relative
`O_NOFOLLOW` export avoids following concurrently swapped directories. Import
builds a new sibling tree, fsyncs files and directories, then uses Linux
`RENAME_EXCHANGE`; it never merges into the current app directory. The caller
must retain its durable recovery archive until verification and commit.

Owner-home hardlinks can be flattened only after a descriptor-relative inode
inventory proves every link is inside this owner's home and the linked file is
UID1000. An external link, another filesystem, or incomplete inventory rejects
migration. The default root-only exporter remains strict. The publication
exporter remains strict and does not use this exception.

The canonical digest includes sorted paths, entry types/modes, relative-link
targets, and file contents; ZIP order, timestamps, and compression do not affect
it. Export/import/export verification therefore detects meaningful tree changes.

Legacy byte transport is bounded to 256 MiB compressed, 4 GiB expanded and
128 MiB per file. The file-backed `/export/private-workspace-v2` and
`/import/private-workspace-v2` transport supports 4 GiB compressed, 8 GiB
expanded and 1 GiB per file, with 200,000 app entries, depth 32 and bounded ZIP
index metadata. Migration uses this streaming version. Files spool to private
disk instead of an unbounded in-memory archive; staging still needs room for
both old and new trees plus the ZIP. These limits are explicit rejection bounds,
not a disk-capacity guarantee or truncation policy. Owner-home inode inventory
remains bounded to 1,000,000 entries.

Only the app root is transferred by this transport. Home-level tool caches,
user dotfiles, agent history, and supervisor state are not implicitly included.
The migration inventory must classify these separately. Never copy old runtime
control credentials or global model credentials into a new guest. Finished canonical task history uses the selected-task channel below without
copying a live supervisor identity. Additional owner files use the explicitly
reviewed [private home manifest](private-home.md).

## Changed dependencies and Git

Sanitized source imports prepare a fresh staged destination before exchanging
it into the app root. Matching fresh-template Node manifests keep the existing
fast cache path; matching Python requirements keep the fresh `.venv`. Local and
workspace package references force fresh dependency preparation instead of
assuming root-only manifest equality is enough.

Changed dependencies support npm lock/shrinkwrap (`npm ci`), pnpm frozen locks,
Bun frozen text locks, unlocked npm/pnpm package manifests, and Python
`requirements.txt` into a private `.venv`. Python script shebangs are rebased to
the final app path. Yarn is explicitly rejected until its reviewed image/tool
contract is implemented. Binary Bun locks and hidden package-manager config are
not part of the current sanitized publication format.

Installation runs in the new owner guest with a fresh HOME and a minimal
environment. It receives no application config, supervisor credential, Git PAT,
model key, npm user config, or ambient proxy settings. Scripts are ordinary
untrusted guest code; the worker's separately validated egress policy still
controls network access. No registry access is enabled merely by importing.
The eight-minute install budget cancels the subprocess group; failed preparation
leaves the previous app tree in place. Exact-cache imports retain their quick
path; cold dependency changes necessarily take longer and must be represented
truthfully in product progress/error handling.

Cube Git import supports explicitly reviewed HTTPS hosts (`github.com`,
`gitlab.com`, `bitbucket.org`) and a validated single branch, no submodules/LFS
smudge. The host fetch pins a validated public IPv4 address using Git's
`http.curloptResolve`, disables redirects/proxies/global config/helpers, and
uses an owner-scoped, host-matched PAT only through a temporary askpass file.
The guest receives a tokenless checkout and installs its own dependencies.
Git's documented settings are at https://git-scm.com/docs/git-config.
Custom Git servers, redirect-only URLs, and arbitrary imported preview ports are
rejected rather than reaching host/internal services or showing a broken link.
A missing `sandbox.yaml` inherits the explicitly selected preset; a conflicting
web port is rejected before a Cube VM is created.

Host cloning remains bounded by a five-minute timeout but requires an operator
quota on the scratch filesystem for untrusted repository size. Large repository
or artifact support must preserve this disk bound as well as archive limits.

## Validation

`bash scripts/check-cube-workspace.sh` compiles the guest tests, then runs actual
npm/pnpm local-package installation with **network disabled** in the base image.
It verifies installed modules, sterile lifecycle environment, failed-install
rollback, process-group cancellation, Python cache reuse, detached-session
quiescence, and UID1000 private import/export/resume with mutation fencing.
`CUBE_WORKSPACE_TEST_BASE_IMAGE` selects the reviewed image under test; an
optional `CUBE_WORKSPACE_TEST_REPO` selects a Docker-shared checkout path.

Run `bash scripts/check-cube-migration.sh -race ./internal/runtime
./internal/gitimport ./cmd/runtimed ./internal/api` (on one shell line) for the
focused race suite. The private-archive tests cover ancestor symlinks, path/link
attacks, owner-home hardlink proofs, interpreter-leaf policy, canonical digests,
atomic replacement, and unavailable transport error handling. The optional
`CUBE_ARCHIVE_LARGE_TEST=1` fixture verifies a 300 MiB expanded zero-filled tree
roundtrip; its tiny compressed size is deliberate and is not a production load
benchmark. The Git tests inspect pinned-address/no-redirect credential handling;
they do not claim an end-to-end live private-repository import.

## Selected task history across migration and rollback

The private task-history transport copies only selected canonical ULID task
folders and only `events.jsonl` / `result.json`. Each result must identify that
task and be terminal. The migration coordinator obtains the IDs from canonical
sandbox task rows, checks the returned archive contains exactly that set, and
journals a separate checksum. No live supervisor config, transport token,
model credential, session file, or raw adapter log is copied.

`POST /export/private-task-history` accepts `{"task_ids":[...]}`;
`PUT /import/private-task-history` accepts the bounded ZIP. Both require the
same authenticated, quiesced guest. Imports merge finalized task directories
atomically, preserve unselected history, and reject conflicting existing files;
identical retries are safe. Empty history is valid. Missing selected task
history or a nonterminal/mismatched result fails explicitly. Limits are 10,000
tasks, 64 MiB compressed, 256 MiB expanded, 32 MiB per event log and 2 MiB per
result. These limits never silently discard events.

Checkpoint IDs are stored in `result.json`; the corresponding commits and
`refs/sandboxd/checkpoints/` are already part of the private app `.git` transfer.
A regression test transfers workspace plus history, invokes the destination
revert handler, verifies restored file contents, and replays the imported events.
Task finalization now persists terminal history before exposing `done=true`,
so quiescence cannot race the final result write. The UID1000 integration runner
also exercises authenticated-route fencing and export/import/result retrieval.

Imported task directories receive a local `.history-imported` marker, including
identical existing directories merged during rollback. These histories do not
imply a resumable Claude/OpenCode conversation on the destination. The next task
starts a fresh agent session using platform chat/project context; subsequent
locally completed tasks restore normal continuation. Provider session/auth
files are deliberately not migrated. The marker is local runtime metadata and
is not included in the canonical task-history archive or checksum.

Private export first attempts the strict single-link path, so ordinary imported
workspaces do not inspect unrelated home caches or mixed-device overlay files.
If an app actually contains multiply linked files, export still requires the
complete same-owner, same-filesystem inode proof. Newly created hardlinks across
a guest overlay's differing inode devices remain ineligible.

Rollback also protects retained legacy runtimed binaries that do not understand
imported-history markers. The rollback transaction records all imported task IDs;
the API forces a fresh session for each agent until a new local task from that
agent succeeds. Rejected submissions and failed tasks do not consume this guard,
and caller-provided `continue:true` cannot bypass it. Canonical history remains
available throughout.
