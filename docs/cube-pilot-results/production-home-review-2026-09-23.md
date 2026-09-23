# Production owner-home compatibility review — 2026-09-23

This read-only pass accounts for all **56 projects/sandboxes and 30 published
snapshots**. It prepared a candidate home manifest for every sandbox and proposed
React Pro assignments for the nine projects without a stored runtime preset.
**It did not migrate projects, change presets, delete data, or approve production
cutover.** The [sanitized per-project report](production-home-review-2026-09-23.json)
records capability evidence, unresolved checks, fleet identity and private
artifact hashes.

## Results

| Check | Result |
| --- | --- |
| Candidate home manifests | 56 |
| Current Go inventory passes | 48 preliminary passes |
| Home link policy failures | 8 projects, 36 links |
| Unsupported app-root links | 0 |
| Custom native tools requiring ABI/application checks | 4 projects; 3 also have link failures |
| Unique projects with concrete link or native-tool checks | 9 |
| Stock shell/Git defaults matching reviewed hashes | All 56 |
| Other inventory reasons | None: no detected file-size, task-history, active-task, source-container or workspace-layout blockers |

The nine preset proposals all have React, Vite, Tailwind and Radix dependencies,
plus an actual `sandbox.yaml` using pnpm/Vite on port 3000. This establishes a
compatible **candidate** React Pro capability bundle; it does not authorize
replacing owner files or startup commands. Retaining existing presets and adding
these proposals yields 42 React Pro, 12 React/Vite, one Express and one Next.js
project. Every app has a current sandbox.

The manifest candidates preserve owner-editable stock files, caches, tools,
custom source/backups, encrypted corpus data and workspace siblings. Verified
stock bytes are recorded as evidence, but the files use `preserve` so subsequent
owner edits can survive rollback. Known provider/authentication state stays on
the retained private source. Canonical task history and runtime identity use
their separate channels. Provider conversations retained on Docker are not
represented as transferred to Cube.

## Concrete compatibility work remaining

- One unpacked browser/OS library tree contains 28 absolute links into OS
  configuration, font/systemd paths, `/etc/environment` and `/dev/null`.
  Preserving its bytes alone does not prove the target operating-system layout
  or behavior matches. Do not dereference these links on the host.
- Five pnpm project-index links escape the owner home into `/tmp` or a temporary
  tool directory. They currently fail closed. Review their actual role before
  selecting an explicit compatibility rule; this report does not classify them
  as disposable or remove them.
- Two uv cache interpreter links require `/usr/bin/python3.13`; a custom Python
  environment has one `/usr/bin/python3` link outside the currently reviewed
  `.venv` rule. Exact interpreter/venv compatibility must be established before
  extending a private transfer rule.
- Four projects contain custom native tools: unpacked browser/system libraries,
  a standalone Chromium tree, image tooling with native Node modules, and a
  Python/NumPy environment with an additional executable. The 277 inspected ELF
  headers identify x86-64. That proves architecture only, not glibc, shared
  library, Node ABI, Python ABI, or application compatibility. Execute their
  checks only inside a properly isolated target guest after preserving all data.

Other custom roots contain owner source/backups and data; they are included in
candidate manifests. Nothing was classified as disposable. Path-level link
metadata and native-header evidence remain private rather than being published
in this report.

## Validation and artifact handling

The scanner used SQLite `mode=ro`, filesystem metadata, bounded stock-file hashes,
selected package/startup capability extraction, and native file headers. It did
not execute tenant code or emit file contents or credentials. The current
`cube-migrate inventory` and `fleet-preflight` binaries ran with networking
disabled, two CPUs, 2 GiB RAM, and read-only production mounts. Reading owner-only
directories required `DAC_OVERRIDE`; no production write mount was supplied.

Private artifacts are under the root-only directory
`/opt/baarcha-bench/cube-global-20260923/dependency/fleet-review/`:

- `candidate-home-manifests.json` and `candidate-preset-assignments.json` are
  operator-review inputs, not automatically approved migration instructions.
- `inventory-private.json` and `fleet-private.json` contain actual Go preflight
  results. The sanitized report binds the private artifacts by SHA256.
- `review-private.json`, `links-private.json`, `native-headers-private.json` and
  `custom-paths-private.json` retain the detailed evidence for review.

No reviewed production template map was supplied to this pass. Fleet preflight
therefore correctly blocks all 56 projects and all 30 snapshots on missing
reviewed target templates. The 48 inventory passes must not be called production
readiness. Admission freeze/task drain, stopped-source ownership and hardlink
proof, disk headroom, immutable archive verification, target image capabilities,
private network/model routing, UI/preview behavior, application smoke checks and
rollback roundtrips remain required. Published snapshot conversion is a separate
rollout requirement.
