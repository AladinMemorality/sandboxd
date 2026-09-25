# Retained unavailable source: owned running-loss continuation

The corrected worker retained the synthetic provider
`524905c6b8a84f9b90470e0f0a813894` after the running-loss test. Native recovery
failed with `Cube runtime requires recovery`. Its observed new worker boot is
`1574a104-22ab-4e85-92ba-36163537171f`. This is a separate result from the earlier
r4 missing-provider404. Neither branch is reported as native recovery success.

The prepared journal binary supports both branches, with distinct evidence
purposes. Retained-source acceptance requires an actual typed unavailable error,
one complete Master node/one exact original provider, no tasks, current archive
provenance and a fresh operator fence. After replacing and verifying the latest
files/SQL and atomic stable binding, the fixture deletes only its newly created
target. It rechecks the original provider and reports
`original_source_retained_verified:true`, `provider_inventory_zero:false`.
This document records preparation, not execution of that new continuation.

## Current capture and rescue

Do not reuse r4 wrappers: they contain old IDs, old boots, old metadata paths and
service-start/stop operations. The new crash stage is
`/opt/baarcha-bench/cube-crash-running-de3-01`. Preserve both `metadata-before/`
and `metadata-after/`, along with original private escrow, report and probe.
Compare their exact current volume path/name/generation, original upper mapping,
image references and independently recorded disk identity before capture.

The current `metadata-before/plan.json` already incorporates template snapshot
original-upper identity. It does not need the historical r4 amendment. After
root has independently fenced controller/sweeper/management requests and proved
no live tasks or writable disk descriptors, issue a fresh worker-local
`CUBE_CURRENT_DISK_CAPTURE` receipt for this ID and its exact before/after boots.
Then use the unchanged capture entrypoint:

```sh
python3 /root/cube-production/recovery-tools-20260925/capture.py \
  --plan PRIVATE_NEW_METADATA/plan.json --fence PRIVATE_NEW_CAPTURE_FENCE.json \
  --output /data/cube-recovery/current-running-de3-01
```

The worker-local path is illustrative; use the exact escrow location verified by
the coordinator, never an old path substituted by a guessed ID. Capture opens
only the retained current disk read-only and creates an independent XFS clone;
no guest filesystem is mounted on the worker. Preserve the original capture
manifest and copy all artifacts independently to a new outer private input.
Verify full hashes after transfer, not merely virtual disk size or file names.

Use a new rescue input/work directory and the tested NIC-down/restore wrapper.
Run `rescue_export.py`, then `archive_convert.py` with the new fixture's exact
latest JSON and canonical Go validator. No earlier baseline ZIP participates.
Retrieve and hash `home.tar`, export-report and converted archives independently.
Keep original disk, full metadata and raw/derived archives outside the worker.

## Fresh journal preparation (no execution)

Reviewed binary:

- `/opt/baarcha-bench/cube-journal-repair-prepare-20260925/control-plane/journal-acceptance`
- SHA256 `83815957dbe4cf5d09cd9445dc8942c391c47e6f58c96645052742ed039b3a49`

`prepare_retained.py` creates a new private evidence directory and generated
command description only. It never starts services, creates a guest, copies a
live source disk or executes the journal command. It verifies authenticated
provider ID/template/resources/fixture metadata and unknown/stopped state;
redirects and environment HTTP proxies are disabled for the API credential.

After the source has been captured and management is available for the explicitly
owned replacement, retain the source mutation fence. Supply a fresh operator
receipt, not one inferred from metadata or timeout:

```json
{"purpose":"OWNED_RECOVERY_EXECUTION_FENCE","old_provider_id":"524905c6b8a84f9b90470e0f0a813894","worker_machine_id":"EXACT_MACHINE","previous_boot_id":"ORIGINAL_ESCROW_BOOT","current_boot_id":"7ee095fe-0461-4779-93e6-42e2b557440f","no_task_verified":true,"no_owned_vmm_or_disk_fd_verified":true,"provider_requests_drained":true,"management_fenced":true,"checked_at":0,"expires_at":0}
```

Set checked_at to the actual fresh inspection time and expiry at most20minutes
ahead. `management_fenced` means old-source mutations remain disabled while the
coordinator alone may create its new owned replacement. It is not permission to
reconnect/delete the source or a claim that a Boolean implements fencing.

```sh
python3 /opt/baarcha-bench/cube-journal-repair-prepare-20260925/prepare_retained.py \
  --crash-stage /opt/baarcha-bench/cube-crash-running-de3-01 \
  --capture-metadata EXACT_MANIFEST_BOUND_METADATA \
  --post-capture-reboot VERIFIED_CONTINUITY_JSON \
  --capture-input NEW_OUTER_INPUT --capture-fence SAVED_CAPTURE_FENCE \
  --export-stage OUTER_EXPORT_DIR --execution-fence FRESH_OPERATOR_JSON \
  --output /opt/baarcha-bench/cube-journal-running-de3-01 \
  --binary /opt/baarcha-bench/cube-journal-repair-prepare-20260925/control-plane/journal-acceptance \
  --binary-sha256 83815957dbe4cf5d09cd9445dc8942c391c47e6f58c96645052742ed039b3a49 \
  --owned-id 524905c6b8a84f9b90470e0f0a813894
```

OUTER_EXPORT_DIR contains home.tar, export-report.json and converted/. The input
contains current.ext4 and the unmodified rescue-input.json. The generated
prepared-command.json includes exact argv/cwd; review and run separately under
2CPU/2GiB, TasksMax128,21minutes. The coordinator retains the failed source even
on success and never substitutes that source ID with a new provider silently.

## Separately reviewed synthetic source cleanup

Do not execute source cleanup until independent full disk/lower/metadata escrow
and latest app/home/SQL plus journal verification are complete and root has
reviewed them. Preserve the historical `1a147...` disk and every unrelated guest.
Recheck exact ID/template/fixture/resource identity and complete inventory.

Pinned CubeAPI `kill_sandbox` forwards authenticated DELETE to synchronous Master
Destroy without requiring running/paused. Cubelet Destroy handles terminated
UNKNOWN/EXITED contexts and tolerates missing tasks; `destroyContainer` skips
pre/post-stop hooks for terminating contexts. Normal workflow destruction then
removes that ID's storage/metadata. This is the supported operator cleanup path,
not an unsafe reset or direct DB edit. Its success on this new owned source must
still be observed; do not assert that source tracing proves live DELETE success.

The platform admission client intentionally refuses Delete on unavailable IDs.
For this synthetic source only, an operator can use exact raw authenticated
CubeAPI `DELETE /sandboxes/524905c6b8a84f9b90470e0f0a813894` after root review.
Do not add a general tenant bypass or alter journal quarantine. Require204,
independent subsequent404, complete Master inventory0 and Cube tasks empty;
record the source cleanup separately from journal target cleanup. A failure
stops for inspection with all archives retained, never manual force-cleanup.

## Explicit post-capture reboot continuity

The retained-source branch alone accepts a separately hash-bound
`post-capture-reboot.json` if execution occurs after another clean worker boot.
It binds original source/provider/machine/data identity, capture and execution
boots, source plan/manifest/fence hashes, and equal full captured/post-reboot disk
hashes. It requires affirmative orderly shutdown, unchanged critical metadata,
no tasks or owned VMM/disk descriptors, and drained provider requests. The
historical missing-provider branch still rejects this extension. Original
receipts are never rewritten; a fresh execution fence is separately required.

The actual running-de3 capture manifest binds `metadata-before`, despite a
separately preserved fresh metadata-pre-fence inspection. `--capture-metadata`
checks the plan and both canonical metadata hashes against the manifest; do not
substitute the fresher metadata merely because its identity fields match.

The 2026-09-25 rescue attempt stopped safely at default filesystem preen with
exit4 before mounting/exporting. Read-only diagnostics identified an unattached
8-byte regular inode (association unknown) and allocation inconsistencies,
including the PostgreSQL replication-origin checkpoint inode. An explicit
clone-only repair requires separate review; no journal continuation or latest
SQL preservation is claimed from this failed export.

## Explicit disposable-clone filesystem repair

Default export still runs preen and refuses exit4. The reviewed optional
`rescue_export.py --repair-current-sha256 EXACT_CAPTURED_SHA256` is an operator
repair request, never a fallback automatically selected after failure. It makes
another fresh scratch inode from the independently hash-verified input, requires
its pre-repair hash to match, saves full private `e2fsck -fy` output, and accepts
only exit0/1. A separate read-only `e2fsck -fn` must return0 before any mount.
A failed repair/check retains its scratch, logs and failed receipt for review.
Inputs, first failed scratch and all earlier outputs remain untouched.

Successful repair saves `repair-receipt.json`, `repair-fsck.log` and
`verify-fsck.log`. Independently retrieve all three alongside the whole-home
archive, export report and converted archives. The report binds receipt and
repaired-clone hashes; journal evidence binds the receipt and both log hashes,
requires the exact command/return-code sequence, and rejects undeclared repair.
The latest app/home markers and PostgreSQL SQL expectations remain unchanged.
Even a successful repaired replacement is recorded separately from failed native
recovery; an orphan's unknown association must not be represented as harmless.


The explicit repaired-export run subsequently passed inside the restricted rescue
VM at14:30:12–14:32:29UTC. Repair returned1 and the separate check returned0;
the unknown8-byte orphan was reconnected under lost+found, outside exported home.
Original current-disk SHA remained974163c7…; all mounts were released. Independent
outer verification preserved twelve output files under
`/mnt/nvme/baarcha-cube/recovery-running-de3-20260925/verified-export`, including
repair/log evidence. Latest app/home markers and canonical archive contracts
passed. PostgreSQL SQL recovery and journal execution are still separate pending
checks; neither native nor production recovery is claimed by this export.


## Actual repaired retained-source journal acceptance

The separately authorized journal ran at14:40:16–14:40:48UTC, exit0, CPU14.879s,
using candidate83815957… and a fresh execution fence with the exact lifecycle
manager container frozen. It verified the ORIGINAL latest acknowledged app/home
markers and PostgreSQL SQL, without recommitting them; quiesced archive digests,
frozen config, synthetic task history and stable owner/app/sandbox identity also
passed. The isolated binding switched atomically and repeated Commit succeeded.
This was an isolated controller database, not a production binding or browser
routing test; original crash task history was not exported by this fixture.

The fixture created target3c545045bcf84e9785113a6700ba60ca, then deleted only
that target and verified404 with zero target admission charge. Independent
post-run inventory showed exactly the original524905… in exited(failed), no Cube
tasks, and the original lifecycle-manager container still paused. Original
source cleanup/unfreeze remains a separate root-reviewed action. Native recovery
still failed; this is current-disk repair plus replacement/journal recovery.

The initial prepared command referenced converted archives under /mnt/nvme,
which the binary correctly disallows. This was caught before invocation. All
four converted files were independently copied under the private journal stage;
the original command was preserved and the corrected command explicitly reviewed
before execution. Future preparation now makes that copy and generates a path
within the executable guard; tests reject outside-root commands and symlinks.
