# Composed v4 starter acceptance — 2026-09-24

**All eight prepared starters passed** their composed v4 functional acceptance in the isolated nested cluster. The [seven non-database rows](functional-report.json) complement the separately linked PostgreSQL lifecycle report. The image combines the current supervisor/source-publication policy, three pinned Vite fixes, and the optional PostgreSQL recipe. Its ABI derives from the reviewed production base; the existing seven preset manifests keep their database-free startup behavior. No production template mapping, tenant project or admission flag changed.

The shared composed image is `sha256:3fd511ce3b190865f49ea3875c8743daffd7c80930d892a073b9b3e03b06aa0f`. Each prepared template is configured for **1 CPU, 1,024 MiB RAM and a 10 GiB writable layer**; outbound guest NIC traffic remains denied. These template IDs belong to the test cluster and cannot be copied into production as if they existed there.

| Preset | Candidate template |
| --- | --- |
| node-postgres | `tpl-2aa3ca52b0d84b3c8e49085d` |
| react-pro | `tpl-f985e6e704684df4945b989e` |
| marketplace | `tpl-ea87516417594bcbba062802` |
| react-vite | `tpl-225b69b14f544892b5f23e6b` |
| nextjs | `tpl-3d684e8c416049289685319e` |
| node-express | `tpl-45e214308a344823b19f8555` |
| fastapi | `tpl-43b96d1e5b9441f48c0a15f1` |
| worker | `tpl-9592dcb244f24e21bf82119b` |

[Exact image digests and template identities](candidate-templates.json) and [compiled binary/source identities](build-identity.json) are recorded separately.

For the seven non-database starters, the [bounded fixture](../../../ops/cube/functional/2026-09-24/preset-matrix/) checks the authenticated supervisor and reverse channel, the declared frontend/API/worker behavior, pause/resume with unchanged supervisor boot time and process PID/restart count, then strict quiesced home-v2 and workspace-v2 export with canonical digests. React rows check HTML, the transformed entrypoint and Vite client; Next.js checks rendered HTML/asset references; Express/FastAPI check root and health; worker has a running process and no web preview. The broker in this fixture rejects all outbound dials. Every owned VM is deleted and a separate provider GET confirms HTTP 404.

The explicitly selected PostgreSQL starter has its own [actual SQL acceptance](../postgres-candidate-2026-09-24/README.md): writes survive pause/resume, reexec, explicit manifest activation, owner source restore and full cold home export/import; a source remix starts with an empty database. The prepared image includes the helper for deliberate future opt-in by other stacks but does not start or initialize PostgreSQL for those stacks. The separate [React Pro candidate benchmark](../react-pro-candidate-2026-09-24/README.md) records timing under a quiet, matched resource window; this matrix is functional acceptance rather than a speed benchmark.

## Capacity and artifact cleanup

The first attempt to build the Next.js template stopped at Cube's disk-headroom check. The [original failure log](nextjs-template-watch-insufficient-space.log) is retained. The builder needed approximately 14 GiB of temporary space and the test data filesystem had slightly less than its exact requirement. No disk-safety margin was reduced and no guest was rebooted.

Two accidentally duplicated PostgreSQL/React template records were created by the initial orchestration list; their [exact identities](duplicate-template-records.json) are retained and both were deleted through Cube's normal API. The tested candidate mapping above retains the original IDs.

The seven older synthetic starter templates, whose image identities and successful checks remain in the [historical matrix](../reviewed-preset-matrix-2026-09-24.md), were then deleted through the template API after inspecting all current guest references. The sole unrelated guest was already paused on September 17, before those templates existed, with its own XFS pause snapshot and no template reference. The [cleanup record](old-template-cleanup.json) shows an identical guest inventory before/after and free data space increasing from 15,500,095,488 to 40,238,559,232 bytes. Its guest and snapshot were preserved. No global image prune, manual rootfs deletion, production deletion, or disk-layout change occurred.

## Reproduction and limits

The [fixture directory](../../../ops/cube/functional/2026-09-24/preset-matrix/) contains its exact Go source, prepared-image Dockerfile and operator orchestration scripts. Compile the fixture under `control-plane/cmd/reviewed-pilot`; it requires the isolated cluster's private credential file and marker. Build inputs/logs remain under `/opt/baarcha-bench/cube-global-20260924/postgres-v4/` on the outer test host. The fixture executable SHA256 is `c9c59debfcb2ac16f23a68f6e91259265831263ed042adc024b5a2d6b0585bfc`.

The candidate's baked PostgreSQL README predates the final host-side manifest-activation paragraph; the canonical image Dockerfile now copies the current recipe. Executable guest code is unchanged by that documentation update. Prepared FastAPI adds its normal virtual environment/dependencies before template creation.

This is fresh-starter acceptance on one isolated host. It does not establish all existing owner applications, migrated native ABI, full browser interaction, external-service behavior, production capacity, off-host recovery or deployment isolation. The non-database rows export owner data but do not repeat a full import/rollback for every preset; that scope has separate recovery fixtures. The final independent inventory shows only the original paused guest remains, with 25,952,313,344 bytes free on `/data`. No fixture guest or build job remains active. Production migration gates remain unchanged.
