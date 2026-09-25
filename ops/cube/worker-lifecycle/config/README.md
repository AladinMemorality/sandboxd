# Private lifecycle configuration contract — inert

These schemas describe the candidate's reviewed host and nested manifests. They
are review artifacts, not automatically executed by `lifecycle.py`. Its native
preflight still checks identities, hashes, filesystems and current service state;
JSON schema validation alone cannot attest any of those facts. Unknown fields
are rejected by the schema so the reviewed manifest has no hidden configuration.

The host and nested VM deliberately both use `/etc/baarcha-cube/lifecycle.json`
**in different machines**. Never copy the host manifest into the nested VM or the
nested manifest onto the host. Configs and receipts must be canonical real files,
root-owned mode0600; their parent directories root-owned0700. The examples are
intentionally invalid and cannot be approved manifests by copying them unchanged.
Neither manifest contains API credentials. The separate host
`/etc/baarcha-cube/worker-stop.json` does contain an operator-only Cube API key;
never print, commit or put that file in a public diagnostic bundle.

- `host-lifecycle.schema.json`: exact QEMU hash, outer NVMe mount UUID, reviewed
  lifecycle/drain integration, and the absent-status initial-empty branch.
- `nested-lifecycle.schema.json`: exact machine/data UUID, exactly three patched
  binaries, actual launch/config/unit artifact hashes, and reviewed persistent
  metadata roots. Record every stateful dependency's real path; placeholders are
  not locations. All paths must be canonical without symlink ancestors. Runtime
  metadata checks require the actual `/data` device, not merely a `/data` prefix.
- `initial-empty-review.schema.json`: a private operator review record, **not
  consumed as authorization by current code**. It accounts for every retained
  fixture disk as well as empty provider/runtime metadata. Empty API inventory
  after metadata loss is insufficient. No record generated here is tenant-ready.

Record immutable binary hashes only after the final metadata/retention/network
patch build is installed and independently inspected. Hash every actual start and
stop script, management config, reviewed target/drop-in and dependency image
contract selected for this deployment. Do not hash this manifest into itself.
Paths are prescribed evidence subjects, never commands to run from the manifest.

Keep the following scopes distinct: the outer data filesystem UUID identifies
the host NVMe volume holding `data.qcow2`; nested `data_filesystem_uuid` identifies
XFS **inside that image**. QEMU PID/starttime belongs to the outer host; worker
machine ID and boot ID belong to the nested VM. A boot ID changes after every
worker reboot, but machine ID and data UUID must remain the reviewed values.
