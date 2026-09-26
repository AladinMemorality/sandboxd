# Owned React build profile

This is a deterministic workload for the existing operator-owned notes canary. It does not submit an AI task or measure model intelligence. The reviewed run on September 25 passed and restored the original manifest and services. Unit tests remain separate from that actual execution; see `results/actual-2026-09-25.md` for measured scope and limits.

The guest runner uses the reviewed `/opt/templates/react-pro` package and prepared dependencies, copies them to an entirely separate nonce-owned directory, then generates a searchable/filterable React inventory with 48 imported TypeScript feature modules, the existing component kit and Zod. The notes source, PostgreSQL storage and task history are not used as build inputs. The runner refuses UID 0 instead of relying on the broken operator exec/stdout path.

## Measurement contract

| Phase | What is measured | Important limit |
|---|---|---|
| Copy/preparation | Bounded template/dependency copy and fixture generation | Report separately from compilation |
| New frontend process | Start Vite and local API; fetch HTML, entry, 48 transformed feature modules and valid API records | Not a cold VM, empty page cache, or dependency installation |
| Warm API | Eight requests through Vite's local proxy | Exact synthetic response validation |
| Build | Existing `npm run build --offline` (`tsc -b && vite build`) | Prior artifacts removed only from the copied workspace |
| API during build | 64 requests, concurrency 1, at most four starts/sec | Report actual timing overlap; it may outlast a short build |
| HMR | Change only the owned marker, receive Vite's real websocket update and fetch new transformed bytes | No browser-render assertion |

Every phase records wall and monotonic timestamps in `output/report.json`. Use the existing exact-guest resource sampler for simultaneous guest/VMM CPU, memory and I/O. The Python child RSS field is a cumulative `RUSAGE_CHILDREN` high-water mark, **not** total guest or concurrent process-tree RAM. No 1 GiB profile or global capacity change is included.

Bounds: UID 1000, fixed loopback ports 3012/3013, no install command, sterile child environment without model/bridge credentials or proxy settings; copy at most 100,000 entries / 2 GiB / 120 seconds; build 180 seconds; total guest runner 480 seconds; one MiB build/probe logs. Long-lived service stdout is discarded. Timeout and normal cleanup kill/reap the exact owned child groups, including descendants whose parent already exited. A persisted output directory refuses a second workload invocation before copy/build; it must never be deleted to retry an ambiguous run.

## Review and staged execution sequence

Each execution requires root's review. Use the existing four outer locks and nested worker fixture lock, pin their reviewed helper hashes and the current worker/controller identity. Do not reuse stale controller IDs from earlier tests. Run sequentially with the recovery-data fixture; each must restore its manifest before the other starts.

1. Verify the exact existing canary app `01M3CZB4HXT2Y8HP8CEY75PCWY`, sandbox `01M3D1Q0E1KM1FEM244XVHEC65`, owner `baarcha:103`, external fixture ownership marker, current immutable Cube binding/template and matching applied configuration revision. Require zero active tasks and precisely the four terminal history IDs in `guest.py`. Do not create a new user/app or expand the allowlist. Confirm notes/PG health and retain the original notes source hashes and current synthetic SQL markers.
2. Read `sandbox.yaml` using the authenticated runtime file API into a new private outer stage, fsync the exact bytes, and hash it. The existing API request must use the reviewed owner/operator client without logging its headers. Read-only template proof already showed the pinned package and Vite/TypeScript binaries exist; the runner checks these again. Refuse an existing nonce directory or occupied fixture ports.
3. Prepare candidate files offline (absolute paths; the output directory must not exist):

   ```sh
   python3 prepare.py --original /PRIVATE_STAGE/original-read.yaml \
     --out /PRIVATE_STAGE/prepared --run REVIEWED_16_HEX_NONCE
   ```

   `prepare.py` preserves existing manifest text and adds only worker `operator_frontend_profile`. Unsupported YAML layouts are refused. Preserve `plan.json`, original bytes and all source hashes.
4. Submit original and candidate to the existing read-only `POST /v1/runtime/manifest/validate` endpoint. Both must be valid. Call `prepare.verify_validations(before, after, nonce)` on the returned JSON; the complete existing effective web/worker configuration must be identical except for the one additional worker. Also review the textual diff so the existing build settings and any fields outside that effective view remain unchanged.
5. With no other writer to this owned fixture, use `PUT /v1/sandboxes/ID/files?path=...` to stage only:
   - `.operator-frontend-build-20260925/NONCE/input/guest.py`
   - `.operator-frontend-build-20260925/NONCE/input/probe.mjs`

   Read both files back and verify plan hashes. Recheck ownership/binding/task-idle and the original manifest hash immediately before installing the candidate. Then invoke `POST /v1/sandboxes/ID/recreate` with `{"reload_manifest":true}` **once**. This launches the worker through the ordinary UID-1000 supervisor, not privileged Cube CLI exec. Adding this worker briefly restarts the existing notes and PostgreSQL processes; their files and definitions are retained.
6. Capture resources with the existing exact-guest sampler. Poll only the nonce's `output/report.json` through the file API. Retain all phases/logs on timeout or failure; do not replay the reload or remove its one-shot sentinel. Archive `report.json`, `ready.json`, `warm.json`, `load.json`, `hmr.json`, build/probe logs and their hashes privately. Successful completion requires `successful:true`, all fixed probes, nonempty built HTML and `owned_children_stopped`. A real browser can be added separately; this runner does not claim pixel/render validation.
7. Restore the **exact original manifest bytes** only after rechecking zero active tasks and verifying the current manifest is byte-for-byte the installed candidate (`prepare.verify_restore`). Keep the same exclusive owned-fixture writer fence across read, compare and write. **The current file API has no server-atomic If-Match/CAS operation**; do not describe this as atomic API CAS or run it concurrently with another writer. Any hash drift means preserve state and stop for review, never overwrite.
8. Reload the original manifest once, verify applied configuration and notes/PG health plus the original source/SQL markers and all four task histories. Archive restoration proof before releasing locks. Keep the nonce directory as bounded evidence until explicit cleanup approval; the runner has no recursive customer-path cleanup command. Do not delete the canary or PostgreSQL data.

The temporary command is exactly:

```text
python3 .operator-frontend-build-20260925/NONCE/input/guest.py --run NONCE
```

The original notes web service continues on its original port. Both temporary listeners bind only guest loopback; no external preview route, management allowance or native NIC policy changes are needed. Source/control files are uploaded and reports downloaded via the supported authenticated runtime file API, so operator stdout loss is irrelevant.

## Coordinator and validation

`run.py CONFIG --check` acquires the reviewed outer and nested locks, verifies source/identity pins, reads the owned manifest and health/history, and validates both effective manifests without changing the guest. `run.py CONFIG --execute` repeats preflight, then runs the sequence above through `profile.mjs`. Config/source files and the new output stage must be private and pinned. The original manifest is retained and fsynced before any mutation. A single private journal persists each pending mutation **before** dispatch. Reinvocation refuses an existing journal; unknown reload completion requires inspection, never a blind retry. Known terminal workload failure still restores the original manifest and verifies health, unless the manifest has drifted.

The wrapper has an 840-second deadline and kills/reaps its own coordinator before releasing locks. The guest has its separate 480-second limit. A separately owned sampler must cover the actual workload; its observation duration is not a claimed coordinator execution deadline. `analyze_phases.py` reuses the reviewed resource analyzer but selects intervals directly from the original guest report's integer `wall_time_ns` markers, not the JavaScript-reserialized journal or an unrelated AI task. Missing short-phase samples remain unavailable. It performs no remote actions.

`results/validation.json` pins the initial tested sources. Thirteen native Linux Python tests passed in a new private outer stage under 1 CPU / 256 MiB / 30 seconds, with the transient unit inactive afterward. Final local coverage passed 17 Python and 10 Node tests. Tests cover external symlink/special-file refusal, bounded copy/logs, credential-free child environment, UID-0 rejection, terminal history, exact manifest preservation/drift refusal, meaningful HMR/API assertions, real timeout/descendant cleanup, mutation journaling, ambiguous reload refusal, and phase-analysis bounds. The actual root-coordinated execution independently proved the prepared-cache build and HMR/API paths.
