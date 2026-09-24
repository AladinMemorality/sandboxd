# Composed React Pro Cube candidate, 2026-09-24

The composed candidate retained the earlier Cube application speeds in this small matched run. The normal environment reload also passed, published/remixed source retained the pinned Vite patch, and the database-free preset started no PostgreSQL process or data directory. No production application was changed.

## Candidate and method

- Image: `baarcha-cube-reviewed-react-pro:20260924-v4-final`, `sha256:82bcbc1f26582cbe3bcb2119d1d462db7d9920aa7575dc4964c49fc42f6c1a43`.
- Disposable template: `tpl-f985e6e704684df4945b989e`.
- The composed base includes the Vite pnpm patch, optional PostgreSQL tooling, and the updated guest source-archive policy. React Pro's process configuration remains database-free.
- Actual Cube sandbox metadata asserted **1 CPU / 1024 MiB** for every source guest, outside the timed creation window.
- One warmup and three measured full lifecycle iterations. A separate guest tested ordinary source/environment edits and absence of PostgreSQL. Other acceptance agents paused heavy nested work for the timing window.
- Same nested VM and handler/HTML/transformed-asset readiness definition as the [earlier matched run](../app-lifecycle-2026-09-24/README.md). Tests ran through the authenticated Cube preview proxy; the guest's direct NIC egress remained disabled and its reverse broker denied destinations.

The operator control-plane source was a working-tree snapshot based on `b74fa7c0286225e6994e6e6b3ecae645b4f903b8`, including pending source-policy/restore changes. Archive SHA256: `d46e78c59fbea68579224617d539648049afb273683ba01bedc6d6f65090ec61`. The reviewed lifecycle fixture was overlaid and compiled afterward; executable SHA256: `19f747e21c69906f727e348f640979898d2a6282571fcab3a65818b4fcb6d4af`. This is not represented as a clean committed-revision run.

## Timings

Median seconds, excluding warmup:

| Operation | Earlier Cube | Composed candidate | Candidate range |
| --- | ---: | ---: | ---: |
| Create to app readiness | 4.511 | 4.429 | 4.413–4.577 |
| Resume to app readiness | 1.149 | 1.171 | 1.105–1.220 |
| Stop plus resume to readiness | 1.345 | 1.363 | 1.300–1.458 |
| Publish plus source readiness | 0.555 | 0.325 | 0.271–0.856 |
| Remix to app readiness | 4.891 | 5.113 | 4.747–5.132 |

The candidate's measured create, resume and remix ranges overlap the earlier ranges. Remix's median increased 4.5%, still within the earlier 4.713–5.134-second range. These three-sample runs support no observed material regression; they cannot establish a statistical equivalence or general production capacity claim. The prior comparison used an older operator source snapshot, and unrelated outer-VPS load was uncontrolled.

## Functional results

`lifecycle-output.txt`: **PASS, 48.29 seconds**. All four iterations verified their source marker and the exact `patches/vite@5.4.21.patch` SHA256 after publication and remix. This covers the new guest source-publication policy that the earlier v3 regression could not exercise.

`edit-probe-output.txt`: **PASS, 7.44 seconds**. On a separate fresh guest, normal source edit readiness took **232 ms**, then `.env.local` edit reached the transformed new value in **887 ms**. Unlike the deterministic cold-race regression, this probe inserted no artificial optimizer delay. A disposable Vite middleware then inspected only guest process names and the expected database-directory existence: **zero `postgres` processes**, **no `/home/sandbox/.baarcha-postgres/data`**. Both reports confirm all owned sandboxes were deleted.

The initial launch failed before sandbox creation because macOS archive sidecar files (`._0001_init.sql`) reached the disposable migrations directory. Only those fixture sidecars were removed before the successful run; this packaging failure is not an application acceptance pass.

This run verifies representative HTML/module readiness, not full browser rendering, screenshot behavior, public production routing, sustained concurrency, other presets, or fleet migration. The separate [cold reload report](../vite-cold-reload-2026-09-24/README.md) covers the original race, exact patch, frozen reinstalls, and deterministic Docker/Cube shutdown regressions. Production rollout remains subject to the other migration and release gates.
