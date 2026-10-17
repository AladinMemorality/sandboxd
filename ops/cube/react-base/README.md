# React Pro Base 2.0.0 release

This replaces new `react-pro` workspaces with prepared marketing, portfolio and
app pages. Marketplace retains its workflows and gains the shared media/motion/
scroll/Three.js modules at image build. Existing customer workspaces are never
reseeded. Next.js, plain CSS, Python and backend presets retain their stacks.

## Validation

From image/templates/react-pro run `pnpm install --frozen-lockfile`,
`pnpm check:prepared`, `pnpm typecheck`, and `pnpm build`.
From the repository root run scripts/check-react-base.mjs with
PLAYWRIGHT_PACKAGE_JSON pointing to an installed playwright-core package's parent
project package.json; CHROME_PATH can override the browser binary. ffmpeg creates
local MP4/HLS fixtures. The script uses a disposable source copy, checks all three
page sets at 390/1440px, media playback/failure, reduced motion and Three.js cleanup,
and writes screenshots/results to REACT_BASE_RESULTS (default /tmp/react-base-results).

The normal image/cube/Dockerfile refreshes both source and dependencies. To release
only frontend templates while preserving a reviewed supervisor, use
image/cube/Dockerfile.react-base with an immutable credential-free Cube base image
and RUNTIME_PRESET=react-pro or marketplace. Never use a customer snapshot as base.
Both recipes compile the pages and install from frozen locks before snapshotting.
Verify the prepared app and a cold preview with container networking disabled.

## Cutover contract

1. Build/register new uniquely named Cube templates using the reviewed preset's
   networking, bootstrap, ports, CPU, memory and writable-layer settings. Journal
   create intent and returned job ID; never retry an ambiguous creation blindly.
2. Require READY on every active worker, and a fresh Cube canary proving supervisor
   startup, prepared files, preview, and source export with its lockfile/patches.
   A Docker build is not Cube acceptance. Verify replicas before re-enabling any
   currently draining worker.
3. Deploy the companion landing changes: composition selection in create_project
   and /api/projects, initial file preparation and accurate task instructions.
   Old templates remain compatible during the staged rollout. Regenerate the
   platform's reviewed starter baseline when changing the prepared page sources.
4. Use plan.py to produce the three-variable Compose overlay from the current
   environment and verified release receipt. It preserves old template entries,
   other presets, budgets and worker draining state. Run `python3 -m unittest
   discover -s ops/cube/react-base` to check the planner.
5. Apply through the existing locked controller release path, retaining exact
   prior config and runtime bindings; update controller lifecycle identity pins
   if recreating it. Verify the normal create_project route receives version 2.0.0
   and its chosen composition before any model task starts. Keep the old image
   and template defaults for rollback. Do not repoint existing runtime bindings.

The planner only writes a reviewable overlay; it never changes production itself.
No fabricated template IDs or canary receipts are checked in.

## Release tooling

`register.py` journals immutable image registration without changing defaults.
`canary.py` runs on the worker beside an amd64 `canary-channel` built from
`channel.go` inside the control-plane Go module. It opens the normal authenticated
reverse channel with a deny-all broker, verifies cold previews and source export,
and deletes its own guest. The Cube bootstrap accepts only its two transport
environment values; do not inject flags to bypass normal startup.

`activate.py --prepare` records the current controller, environment, exact durable
resource contracts and verified replacement plan. `--apply` locks deployment,
requires no active tasks or pending provider operations, backs up state, stops the
controller and takes its database maintenance lock. It adds only the new template
profiles to the durable budget, preserving capacity and every old entry. Compose
must differ only in the three reviewed environment variables. Failure restores
the exact old contract/config; existing runtime bindings are never repointed.
An existing attempt must be reconciled and archived before retrying.

`platform-canary.mjs`, run with the deployed platform environment, creates one
synthetic operator app through the ordinary controller endpoint and calls the
actual platform `prepareStarter` function. It verifies revision, selected pages,
original prompt and preview readiness before deleting that app. No model task
is submitted. Native canaries and this test do not claim corpus-wide acceptance
or verify a full paid coding task.
