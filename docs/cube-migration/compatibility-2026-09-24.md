# Fleet compatibility refresh — 2026-09-24

Production inspection remains read-only. The current inventory contains **58
apps/sandboxes and 30 snapshot records**, rather than the earlier 56. The first
metadata scan observed zero active tasks and 12,789,353,664 app-file bytes. These
are live observations, not a frozen cutover plan.

The existing durable presets are React Pro 34, React Vite 12, Next.js 2,
Node/Express 1 and unspecified 9. The nine unspecified projects still have
candidate React Pro assignments supported by their installed package
capabilities and declared startup-command family. These assignments are
explicit proposals; no production metadata was changed. If reviewed, the
fleet target totals are React Pro 43, React Vite 12, Next.js 2 and Express 1.
Template IDs must come from the tested images; none are invented by inventory.

The final Go inventory validator passes **57/58 preliminary transport rows**.
The only failing row is the excluded MyHomeTroc project described below. With
no reviewed template map supplied, fleet preflight still correctly blocks all
58 projects and all 30 raw snapshots. The nine explicit preset proposals resolve
the old missing-snapshot-preset metadata ambiguity, but remain proposals.

## Owner-home compatibility changes

The refreshed scanner found the same 36 unsupported literal home links as the
previous inspection: 28 unpacked browser/system package links, five pnpm
project-index links and three custom/cache Python interpreter links, across
eight owners. No app-root link failures were reported. Version-two private home
manifests now express an exact path/target/kind contract for each of those links.
The real Go validator then exposed two literal-backslash systemd filenames in
the unpacked package tree; the adapter preserves those two exact regular files
through an explicit `literal_paths` contract. No source file is rewritten,
dropped, followed outside the owner tree or substituted from the host OS.

The separate in-progress MyHomeTroc app
`01M37PPK7VDKCQMNRYKW8CCD4W` / sandbox `01M37PPK85JN1K0WEMP4ZYER6C`
remains excluded from migration/freezing. Its current owner home contains a
special file and deliberately fails the transport validator. Nothing was
removed or edited to make that project eligible.

Twelve owners have custom-tool categories requiring destination application
checks. At least the previously identified Chromium/system libraries, standalone
Chromium, image tooling/native Node dependencies and custom Python/NumPy
environment require real ABI/runtime verification. Literal-link acceptance is
not an ABI attestation. The image workstream is pinning the exact production
base (Node 22.23.2, Python 3.13.5, pnpm 10.34.4, glibc 2.41); base identity alone
does not prove application startup under Cube.

## Private artifacts and cutover gates

Root-private candidate manifests, preset proposals, link metadata and native
headers are under
`/opt/baarcha-bench/cube-global-20260924/compatibility/fleet-reviewed-candidates/`.
They must not be committed or copied into published source. The adjacent
sanitized JSON report records artifact digests and project IDs without owner
filenames, source, prompts, provider state or credentials.

Provider/auth state stays intact on the retained Docker source. Canonical tasks
and Git checkpoints use their dedicated transport; Cube starts a fresh provider
session. Same-owner app data, selected home roots and workspace siblings remain
subject to canonical digest roundtrips and rollback verification.

Remaining acceptance is explicit: reviewed per-preset template IDs, matching
interpreter/native dependencies, target disk budget, actual guest application
readiness, operational egress coverage, offline hardlink proof and complete
workspace/home/history roundtrips. Admission must be drained before the final
inventory and identity-digest check; newly created or changed projects invalidate
the old plan. MyHomeTroc is not covered by a frozen migration plan. No production
sandbox or provider binding was changed by this work.

## Validation evidence

The complete runtime and migration packages pass their Linux race suites; the
authenticated guest home-route race tests pass. A disposable UID1000 guest
fixture exercises v1 and v2 export/import, exact interpreter-link and systemd
literal-filename preservation, malformed/truncated framing, legacy-version
rejection, quiescence and resume. The remote transport tests include a manifest
larger than 4 KiB without an HTTP manifest header. Three scanner fixtures verify
candidate classification, private output modes, credential-content exclusion,
and refusal to traverse owner or workspace-parent symlinks. Tests used isolated
containers with no network and at most two CPUs/2 GiB; no live owner archive was
exported or imported.
