# Separate operator recovery-data fixture (prepared, not executed)

This fixture supplies explicitly **operator-authored** recovery data for app
`01M3CZB4HXT2Y8HP8CEY75PCWY`, sandbox `01M3D1Q0E1KM1FEM244XVHEC65`, owner103.
It does not submit tasks, grant credits, mark the four failed tasks successful,
change provider IDs, use guest root exec, or assert that backup/restore passed.
The original AI journal and task history remain unchanged. Existing owner103
and foreign104 sessions are reused; an expired session fails rather than renewing
credentials. No customer app is eligible.

The one temporary UID1000 manifest worker creates a uniquely named owner-home
folder with a newline/special-character filename, mode0640 file, mode0750 script,
relative symlink, and two names for one inode. The worker creates each file
exclusively and refuses partial/corrupt pre-existing data. It performs no network
calls. It is removed by restoring the exact original manifest; the temporary
worker must not overlap `operator_frontend_profile` from the build benchmark.
Both workflows hold the same four outer deployment/operator locks and worker
acceptance lock. Manifest reload briefly restarts the existing web/PG services.

The original server and HTML are backed up privately before replacement. A
small, exact-anchor server patch reads the actual file/link/mode proof on every
request; the HTML displays an explicitly labelled operator marker. Those marker
patches intentionally remain as source data for the future backup. One row is
inserted through the existing notes API, acknowledged and independently read.
The real private screenshot is saved and checked using owner/foreign/anonymous
file reads and denied unauthorized capture requests. The JPEG still needs
independent visual review; the script never labels byte checks as visual proof.

## Root review and staging

All current files are source-only. Do not execute until root reviews the exact
source and private config and the benchmark helper has restored its manifest.
Stage this directory **and** the sibling `canonical-fixture/{fixture.mjs,run.py}`
under a root-controlled source directory, preserving their relative paths.
The existing enrollment runner supplies the pinned `NestedLock` implementation.
Create a fresh root0700 stage under `/opt/baarcha-bench/cube-operator-recovery-`;
write `config.json` root0600 there from `config.PREPARED.json`. Placeholder values
are deliberately invalid and must never be treated as assertions.

Review inputs are obtained through authenticated read-only API requests and
existing private files: original AI journal hash; the exact current controller
ID/image and Cube runtime ID; worker boot ID; exact original `sandbox.yaml`,
`server.mjs`, `public/index.html` hashes; and the four complete task results.
`task_inventory_sha256` is SHA256 of JavaScript `JSON.stringify` of those complete
JSON results sorted by `id.localeCompare`, as used in `main.mjs`. Do not substitute
task-list summaries. All task status values must still be `failed`. Hash the four
source files named in `source_sha256`, both canonical dependencies, and the
reviewed enrollment source. The runtime/global scope is independently checked
again before every phase and guest file mutation; only this app may be enabled.

Current completed runtime release is `1fcfde24a08b02d7087991fb14fe23060bcf36e3`,
controller `93169a6a71c6d1e35785683418e0c0421674b2fc48baaf973005d542f9c11914`,
image `sha256:74f76d6b6db87b4c710c4c4fbf57db463e15e16f30be6e2abb60ee11fd5119ee`.
These are observations, not perpetual authority; recheck before filling config.
Existing `/opt/baarcha/landing.env` supplies the native coordinator's runtime,
platform database and existing session authority; nothing copies it into a guest.

Run phases separately, inspecting the private journal after each:

```sh
python3 /ROOT_REVIEWED_SOURCE/operator-fixture/run.py --config /ROOT_PRIVATE_STAGE/config.json --action prepare
python3 /ROOT_REVIEWED_SOURCE/operator-fixture/run.py --config /ROOT_PRIVATE_STAGE/config.json --action install
python3 /ROOT_REVIEWED_SOURCE/operator-fixture/run.py --config /ROOT_PRIVATE_STAGE/config.json --action restoreManifest
python3 /ROOT_REVIEWED_SOURCE/operator-fixture/run.py --config /ROOT_PRIVATE_STAGE/config.json --action verify
```

`prepare` saves local source/history receipts only. `install` performs supported
file PUTs (complete readback bounded to2MiB), adds the one worker and requests
`recreate {reload_manifest:true}`. `restoreManifest` only accepts the exact
installed candidate; unrelated changes cause refusal. `verify` reads fresh
filesystem/PG evidence, inserts the single acknowledged row, captures and checks
private access. `inspect` reports only phase names, never secret data.

A durable pending intent is written **before** each mutation. Lost responses,
failed readiness, a changed manifest/source/task or failed ACL leave evidence
for explicit manual reconciliation; rerunning is not a generic repair mechanism.
Do not clear a pending intent based on elapsed time, duplicate the SQL insert,
rerun the original AI journal, or remove the fixture as “cleanup” before backup.
Partial writes are retained; their original bytes and expected hashes are in the
private journal. A successful write followed by lost acknowledgement remains
ambiguous until independently checked.

The locks exclude the reviewed operator/deployment tools. They are not an API
transaction against another actor submitting tasks: the exact private fixture
must have no other writer, and every step rechecks active tasks/source hashes.
The runtime reload also rejects an active task. This is a bounded owned-canary
acceptance tool, not a generic customer installer or fleet maintenance gate.

## Validation and later restore acceptance

```sh
node --test ops/cube/backup/operator-fixture/fixture.test.mjs
python3 -m unittest discover -s ops/cube/backup/operator-fixture -p 'test_*.py' -v
```

19 Node tests cover real filesystem hardlinks/symlinks/modes and tampering,
protocol completion, changed tasks/source, manifest CAS, lost write/reload/SQL
acknowledgements, bounded readback and ACL failure. Four Python tests check the
pinned source helper's ownership/type/mode/link contract. These are local tests;
no production fixture execution or actual backup/restore is claimed.

After the full paired encrypted backup is independently copied/read back and
restored, compare the four failed task results/events/checkpoints, original
stable app/sandbox IDs, actual GET `/operator-recovery-proof`, visible marker,
exact acknowledged SQL row and private screenshot ACL again. The hardlink proof
requires the original inode relationship within the restored filesystem; copying
two equal files is insufficient. A source fixture PASS is distinct from that
restore PASS and from normal AI coding acceptance, which remains separate.

## Exact missing-argument repair (owned run8bb03c01d5527213 only)

The first live install acknowledged manifest reload but the generated worker
command omitted its required run argument. The worker refused before creating
home data. This is a fixture defect, not a runtime/data-preservation failure.
The generator now includes the argument and a direct argv regression covers it.
Do not rerun `install`, reset the original AI journal or clear old receipts.

`repair.mjs` is the separately journaled, single-case continuation. Root reviews
and stages the changed `fixture.mjs`, `main.mjs`, `run.py` and new `repair.mjs`,
updates all source hashes, and adds `argument_repair` to a new private config:
`journal_sha256` (the failed operator journal, not the AI journal),
`manifest_sha256`, `server_sha256`, `html_sha256`, `worker_sha256`,
`app_marker_sha256`. Obtain hashes read-only from the exact installed files and
closed failed-attempt journal, and preserve that original config. The new source
and config are reviewed before running:

```sh
python3 /ROOT_REVIEWED_SOURCE/operator-fixture/run.py --config /ROOT_PRIVATE_STAGE/repair-config.json --action repairArgument
```

Only the command's missing literal `8bb03c01d5527213` argument may change. Both
manifests must validate, with no other effective-process difference. All five
installed source hashes, original task inventory and original AI journal are
checked. A separate `argument-repair.json` records intent→PUT/readback→reload
acknowledgement→actual filesystem proof. Failures retain it and do not change the
original operator journal. Successful proof is written to an immutable receipt;
only then does a compare-and-swap against the saved before-journal hash update
its candidate manifest, installed flag and source proof. Existing done entries
remain intact; `argument_repair` points to the explicit continuation receipt.
The next normal phase is `restoreManifest`, followed by `verify`.

The expanded local suite has31 Node tests and four Python tests. Neither a unit
PASS nor this repair source is evidence that the live repair was executed.
