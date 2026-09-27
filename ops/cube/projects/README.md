# Portable Cube project deployments — implementation candidate

This directory contains source storage tools and a disposable rebuild canary.
It does **not** enable automatic VPS-to-B200 placement. Production still uses
the existing four-slot VPS admission path. Migration 0037 enrolls no projects,
and source import/export endpoints are disabled by default.

## Implemented

- A private source format preserves source and lockfiles while skipping installed
  dependencies before traversal. Explicit data, secret and generated-output paths
  are excluded; unknown sensitive files and unsafe archives fail validation.
- The controller stores immutable source revisions, ordered checkpoint pointers,
  per-worker budgets, fresh host observations and durable deployment charges.
  Concurrent reservations cannot exceed a worker budget. Uncertain operations
  retain their charge; old revisions and changed worker boots cannot activate.
- An owner-scoped `GET /v1/apps/{id}/deployment` reports the latest source revision
  separately from the revision being prepared or served. A stored revision alone
  does not imply that a project is eligible for cross-host deployment.
- Cube create requests can express one operator-selected node. The candidate fleet provider
  selects this field from a durable per-worker reservation and verifies actual
  placement through CubeMaster. Production does not yet use that configuration.
- Opt-in runtime endpoints export a quiesced project or restore verified source
  into a **new disposable target** and rebuild locked dependencies. The target
  stays quiesced. Stateful projects and secret restoration are not enrolled.
- Encrypted immutable S3 uploads and authenticated downloads work with the
  existing production object credentials, without bucket-management permissions.

## Storage contract

The coordinator encrypts both source and manifest before uploading. Keys are:

```
<configured-prefix>/projects/<app-id>/revisions/<revision-id>/source.zip.enc
<configured-prefix>/projects/<app-id>/revisions/<revision-id>/manifest.json.enc
```

The database stores the source key relative to the configured prefix. It stores
the plaintext source hash and manifest. The private transfer receipt additionally
records the encrypted object hashes, bucket and full keys. A future publisher
must verify that receipt before advancing the database checkpoint pointer.

Encryption uses AES-256-GCM with random nonces and HKDF-SHA256 domain separation
from the existing controller master key. Bucket and object key are authenticated.
The master key stays on the coordinator. Its recovery copy remains essential:
source objects cannot be restored after loss of that key. No customer backup
retention or key rotation policy is changed by these tools.

Uploads use conditional creation and read back the full object. An interrupted
retry can acknowledge only identical authenticated plaintext. They never delete
objects, change bucket ACLs, or update a deployment pointer. The CLI always uses
encryption. Plaintext library helpers remain only for explicitly verified private
buckets and are not used by the production-credential canary.

`source-storage-policy.example.json` lists the object operations required by the
encrypted CLI. Existing credentials need no additional IAM grant for the tested
namespace. The earlier bucket-settings 403 did not prevent object access.

## Operator tools

1. Build `control-plane/cmd/cube-project-source` on Linux. Feed it a consistent
   workspace archive and explicit recipe; it writes a new private output folder
   containing verified `source.zip` and `manifest.json`.
2. Run `node source-store.mjs PRIVATE_CONFIG.json` on the coordinator, using the
   usual AWS credential environment/provider chain. Configuration and input
   files must be owned by that process UID and inaccessible to group/other.
   Do not put credentials in the config or pass the master key to a worker.
3. The config selects `operation: "publish"` or `"fetch"`, `version: 1`, `bucket`,
   `region`, `prefix`, `project_id`, `revision_id`, `directory`, `receipt`, and
   `encryption_key_file`. Optionally specify `sdk_package` pointing to the
   installed application package.json. Fetch requires the trusted saved receipt
   and creates a new output directory; it does not extract or run the project.
4. `inventory.py --help` describes the read-only database inventory. Treat its
   output as private owner metadata. Every project initially needs a data audit.
5. `make-canary.py` and `verify-canary.mjs` produce and transfer a secret-free
   operator fixture. Its example image pin is a test value, not a deployable
   runtime identity. Neither tool enrolls customer projects.

Never enable `RUNTIMED_PROJECT_DEPLOYMENTS=canary` on an existing customer guest
for in-place restoration. Import replaces the app tree. It is only for a new
disposable target with `RUNTIMED_PROJECT_IMAGE` set to the trusted pinned image.

## Validation performed on 2026-09-27

- Focused Go race tests pass for source validation, fleet reservations, revision
  ordering, stale observations, owner scoping, runtime transport and API guards.
  The Cube controller builds with Go 1.22 in the existing build environment.
- Eight Node storage tests pass, including object-only credentials, conflicting
  retries, corruption, wrong encryption keys and cross-project authentication.
- The existing production AWS credentials successfully published and fetched the
  encrypted operator fixture in `punicas`, `eu-central-1`. Decrypted contents
  matched the original hash. The tiny fixture round trip took 442 ms; this is
  not a customer deployment latency estimate. Anonymous HEAD on the encrypted
  canary object returned403; this does not establish every bucket policy setting.
- In the VPS runtime image, both isolated guest tests pass. A fixture containing
  a local locked npm dependency rebuilt and served its expected HTTP response
  in about 436 ms after the container was available. Registry downloads and Cube
  boot time are excluded. No customer project was used in this test.
- B200 preflight opened `/dev/kvm` in a bounded CPU-only runc container with no
  NVIDIA devices. Nested virtualization is enabled. This is not yet a complete
  Cube worker acceptance test.
- The same isolated guest tests also pass on B200 without network or GPU access.
  Locked local dependency rebuild plus HTTP readiness took about326ms after the
  container was available. The full source-restore test passed as well. VPS and
  B200 image filesystem/config hashes match:
  `7949e019ffaf2c1af092609410f0f91e01c314e3e00535fabf5e9cff24770adf`.
  Docker's image-store representation differs: VPS identifier
  `sha256:38c1e17d0c108a597668048e45f6d662e6d1c225f598dd96b0256ba20f66873a`,
  B200 imported identifier
  `sha256:0ed224e62909d434480a8d48c0a1a236829279930622208318c8d15efeacba5f`.

Use `cpu-only-rebuild-canary.sh LOCAL_IMAGE_ID RUNTIMED_TEST_BINARY` for the
isolated guest tests. It requires the exact image already present, disables
network and GPU access, and uses temporary workspace storage. It does not run
a Cube VM, register a worker, or publish a preview URL.

## Remaining integration before enabling overflow

1. Provision the bounded B200 Cube worker on NVMe with production isolation and
   recovery patches, private management/preview connectivity, and ready templates.
   Avoid changing the inference host's DNS or GPU runtime. Verify create, stop,
   wake and independent recovery in that worker.
2. Connect durable fleet reservation to actual Cube node selection and verify the
   observed node. Make runtime transport, storage checks, recovery and consumption
   collection worker-aware. Preserve existing live consumption semantics.
3. Add a resumable deployment coordinator: fence the old writer, capture and
   durably publish source/data, provision a disposable target, restore, verify
   health, then atomically switch the binding. Reconcile timeouts and host loss
   without allowing duplicate writers or prematurely releasing capacity.
4. Connect task completion and direct edits to durable checkpoints. Audit each
   project's lockfiles, unavailable packages, mutable data and secrets. Implement
   consistent data/secret restoration before making those projects portable.
   Dependency cache keys exist; cache storage and reuse are not implemented yet.
5. Load-test the configured B200 CPU/RAM/I/O budget alongside inference, exercise
   worker failure and drain, then enable overflow for verified projects and
   report host/revision/checkpoint freshness in admin.

The 73-workspace inventory records locations, not 73 verified portable rebuilds.
Existing full recovery archives remain necessary throughout this rollout.
