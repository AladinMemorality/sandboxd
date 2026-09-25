# Actual corrected-worker functional and paused durability acceptance

All four families below passed on the isolated worker on 2026-09-25. Production
Docker projects were unchanged. Guests used 2 CPUs / 2 GiB; fixture admission
was limited to four active guests. These sequential checks do not measure
four-guest concurrent throughput or production speedup.

| Check | Actual result | Observed elapsed time |
| --- | --- | --- |
| Full PostgreSQL lifecycle | 13 functional/cleanup assertions true | Create through ready 10,664 ms; pause/resume through ready 1,997 ms |
| Publish and fresh remix | Private data excluded; fresh identity and empty database | Publish API 72 ms; remix through ready 4,323 ms |
| Vite 5.4.21 environment reload | Three cold reloads and clean process shutdown passed | 2,286 / 2,229 / 2,193 ms |
| Clean paused-worker reboot | Same guest retained latest app, home and committed SQL | Resume through latest SQL verification 1,021 ms |
| Acknowledged pause then abrupt worker loss | Same guest retained latest app, home and committed SQL | Resume through latest SQL verification 768 ms |

The PostgreSQL fixture also proved supervisor re-exec and changed-manifest
activation preserve the database, identical activation is idempotent, owner
source restore preserves stable app/sandbox/VM credentials, and private home-v2
roundtrip succeeds. Each completed family deleted only its owned guests, then
independently confirmed exact GET404, complete one-node inventory zero and zero
Cube tasks. Five guests in total were deleted across these four families.

Clean reboot used the reviewed nested retained-stop helper followed by QMP
`system_powerdown`; its actual exit status was zero. The paused-loss case used
an exact-process pidfd SIGKILL after the first and only acknowledged pause since
the latest commit, without an operator sync or graceful stop. Both cases
exported independent older app/home backups **before** committing distinct latest
markers. Verification neither imported those backups nor rewrote the latest
markers. Root power receipts, readiness observations and reports are retained
in `clean/` and `paused-loss/`.

## Exact candidate and executable provenance

- Cubelet: `de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b`.
- PG/Vite fixture: `8aa99037878a799e57a4ae6d28558e460788a4b04ec7231dec0d6d6ea89fb513`.
- Clean/paused-loss fixture: `5beb4b8f2033049844c5df512b86b9c38bb79fd645efcc646a4b41942286cf73`.
- Installed guarded Cube CLI: `24fa1fc4846efe74084bf9b27d8c5ce2b0a952f0ad1bb107b25091354fa1d05c`.
- Installed nested helper: `e1e34848f59f507d0e2ae42b23e2887284248ea0454bd739d827ea716f17e205`.

The build records in the parent results directory preserve fixture source hashes,
Linux race checks and build resource limits. `provenance.json` records original
outer-host paths, original byte hashes and hashes of the reformatted committed
JSON. Only explicitly reviewed report/receipt files were copied. No API key,
private credential escrow, home archive or application source is committed.

Readiness required the reviewed persistent metadata roots on XFS, actual open
DB descriptors, all eight exact templates READY, and the 10-CPU/10-GiB native
quota with one concurrent create. Template CLI inspection found eight READY
jobs and one historical FAILED job. The CLI has no complete all-jobs listing;
this is not a claim that unlisted/orphan jobs were independently enumerated.

## Separate running-loss result: native recovery failed, data retained

The separate running guest `524905c6b8a84f9b90470e0f0a813894` committed distinct
latest app/home/SQL at 13:25:16 UTC, after its older backup. No pause, guest
checkpoint, operator sync or repair followed. Its exact metadata was captured
read-only and hash-verified outside the worker before root's pidfd SIGKILL.
After boot, root captured metadata again **before native reconnect** and verified
unchanged sandbox, container, current-disk and lower-image identities.

Native verification failed at 13:32:15 UTC with `Cube runtime requires recovery`.
The original guest and current disk were intentionally retained. This is
**not a native recovery pass**, and latest-marker recovery remains to be proved
by the separately reviewed replacement process. `running-loss/` preserves the
failure plus root power/readiness receipts; no delete, import or fallback was
performed by this fixture. Its executable SHA-256 is
`da10bdfea11ce8f214adac45d7f0ea0e0481f22b56f99ba8f769405613bc5609`.
The private before-metadata manifest hash is
`cad22dbbf4408967291e99877819df131ae12dbd7d6a881e0152d4ea443038ee`;
its credential-bearing metadata stays outside the repository.

## Limits

These results do not prove native recovery of a **running** guest after abrupt
loss, production controller stop/start coordination, recovery-journal rebind,
full-fleet rollout, UI routing, or new network-isolation acceptance. The native
running-loss experiment has a separate guest, stage and evidence. In particular,
`production_stop_coordinator_tested` and `production_accepted` remain false.
The helper's filesystem-readiness field `power_loss_acceptance: false` is its
own narrow scope; the explicit latest-marker paused-loss proof is recorded by
the separate fixture, not inferred from successful startup.
