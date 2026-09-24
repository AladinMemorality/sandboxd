# Actual independent deployment-file recovery, 2026-09-24

`TestOperatorIndependentDeploymentRecovery` **passed in 53.42 seconds** using
real Docker and the reviewed Cube v3 guest. Exact output is retained in
[recovery-test-output.txt](recovery-test-output.txt); executable fixture is
[cube_deployment_recovery_test.go](cube_deployment_recovery_test.go).

This test fills the earlier gap between stubbed independent-file restore tests
and the actual Cube journal roundtrip. It uses only synthetic state and removes
both guest instances and the original synthetic deployment files before restoring
from copied artifacts into a different root directory. No owner project, network
policy, production configuration, model service, or paid request was involved.

## What actually ran

1. Create a private synthetic Docker app with a Git checkpoint, canonical task
   history, private workspace file, retained provider file and owner-home settings.
   Record encrypted runtime config and a private source-ZIP library entry in SQLite.
2. Run the real migration engine into Cube, interrupt after its durable `verified`
   phase, then resume. Revert the transferred checkpoint and create a new workspace
   file through the authenticated Cube supervisor.
3. Roll back current Cube writes and history into Docker, verify stable app/sandbox
   IDs, visibility and preview port, and observe real HTTP 200 from the restored app.
4. Delete the Cube target and original Docker container. Create a consistent
   SQLite `VACUUM INTO` snapshot and fsynced independent copies of the encryption
   key, full owner-home tree, migration archives, published library, deployment
   image/template references and descriptive backup-scope metadata.
5. Close the original SQLite store and delete the entire original deployment root.
   Restore only copied artifacts to a third root and reopen SQLite from those files.
6. Check SQLite integrity, decrypt configuration and runtime binding using the
   restored key, verify the library archive byte-for-byte and validate its source
   format. Verify private files, retained provider identity, owner settings, task
   history and the new Cube write.
7. Explicitly relocate this synthetic sandbox's active workspace and library paths,
   restore UID/GID 1000, and create a fresh Docker instance from the reviewed image.
   Its real HTTP handler checks the private file, owner-home settings, new Cube
   write and decrypted runtime environment, returning only `restored` with HTTP 200.

The original app/sandbox IDs, private visibility and preview port survive. The
historical journal's original container identity remains historical; the fixture
does not rewrite it to pretend that the replacement container was the original.
Both control-store and restored key are reopened from disk; an open original
SQLite handle cannot supply missing backup data.

## Evidence and limits

Source was runtime commit `b74fa7c0286225e6994e6e6b3ecae645b4f903b8` with only this
operator fixture added. Linux Go 1.22 with CGO and vet compiled the migration test
binary in an offline build container. The reviewed image tag resolved to
`sha256:6bad30fa19584d85f0dafc1680bca5851f1bb05f55a6abe0d6002165227253d0`;
the Cube template was `tpl-ce9efc43b71248d9a0adfb90`. Docker and Cube each had one
CPU and 1 GiB memory. The broker denied all generic IPv4 destinations and provided
no model/bridge handler.

An independent post-test check found no Docker containers with the fixture name
prefix and no original/backup/restored fixture directories under `/data`. The
Cube list contained only the previously retained, unrelated paused guest
`6d96feb3819a41aabbcb04e3058b056b`. It was left untouched.

This is recovery into independent filesystem paths on **the same physical VPS
and nested VM**, not an off-host or disk-loss proof. The process exercises actual
SQLite/key reopening and Docker recreation, not a full platform daemon boot or
production HTTPS/DNS routing. Runtime configuration delivery after restoration
is constructed by the fixture from the restored encrypted store. The library
entry is a valid synthetic source ZIP, not a complete production library export.
Task history is synthetic; no new AI task ran in this fixture.

Owner-home backup includes the complete synthetic home, but the Cube migration
leg uses the existing workspace/history transport, not reviewed-home-v2 import.
Owner-home settings remain in the retained source during that leg. This test does
not prove that new Cube writes outside the workspace survive migration; the
separate reviewed-home fixture covers that transport mechanism. Database files,
Unix sockets and active native workers in MyHomeTroc require its own quiescence
and recovery acceptance. Production restore still requires an independently
stored, fresh backup of actual deployment scopes and an operator-reviewed restore.

## Reproduce only in the marked disposable cluster

Copy the fixture into `control-plane/internal/migration/operator_recovery_test.go`
in an isolated checkout of the stated source. Build that package's test binary
with Linux Go 1.22, CGO and cached modules. Run natively from
`control-plane/internal/migration` in the existing marked nested VM:

```sh
CUBE_DEPLOYMENT_RECOVERY_LIVE=1 \
CUBE_MIGRATION_IMAGE=baarcha-cube-reviewed-react-pro:20260924-v3 \
CUBE_MIGRATION_TEMPLATE=tpl-ce9efc43b71248d9a0adfb90 \
./recovery.test -test.run '^TestOperatorIndependentDeploymentRecovery$' -test.v -test.timeout=11m
```

The fixture limits deletion to newly created synthetic deployment directories
and recorded guest identities. Failed cleanup or recovery retains affected
directories for diagnosis. Never substitute a production state path or tenant
workspace. Do not interpret this opt-in result as authorization to change global
admission or deployment isolation attestations.
