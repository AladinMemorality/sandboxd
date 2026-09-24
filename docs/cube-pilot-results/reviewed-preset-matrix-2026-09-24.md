# Seven prepared Cube app presets — 2026-09-24

All seven offered presets passed their prepared-image checks and actual Cube
starter checks in the disposable nested test cluster. This is functional
acceptance of the supplied starters, not authorization or proof for a fleet
cutover. Screenshot workers remain an independent platform service.

The [JSON evidence](reviewed-preset-matrix-2026-09-24.json) pins the base image,
compiled supervisor, each final image and its tested template. These template
IDs belong to the isolated cluster; they must not be copied into production
configuration as if they existed there.

## Isolated cluster mapping

| Preset | Tested template |
|---|---|
| react-pro | `tpl-ce9efc43b71248d9a0adfb90` |
| fastapi | `tpl-16690c3572a04e88a662a8fb` |
| marketplace | `tpl-d2187254ca8d4ea89910c0d1` |
| react-vite | `tpl-c78fd90bc05b42a5a66bd4ed` |
| nextjs | `tpl-7a7173c461ba4846a75c8c3b` |
| node-express | `tpl-849724091697474a97258914` |
| worker | `tpl-6a2b2e0d60da4217bccae0a7` |

Cube ready/resume durations were not instrumented in this matrix. Existing
network-none Docker starter samples are retained in JSON with their exact
scope; they are not Cube timing results. No extra timing reruns were made.

## Checks performed

Each image derives from the exact production ABI base
`sha256:9ec445151344a7baa295b085bebe1a0352ab930601fe0d9ed2bf1283bbaa6678`.
It uses the current reviewed supervisor, credential-free bootstrap, prepared
preset dependencies, and the image-build pnpm-store cleanup already documented
in the [home roundtrip report](reviewed-home-roundtrip-2026-09-24.md).

Every final image ran under UID 1000 with Docker networking disabled. Tests
verified bootstrap initialization and credential rejection, strict baseline
home export, and startup of the preset's declared process with its expected
HTTP readiness or worker-only behavior. The image checks used bounded
resources and did not call external models or services.

Every corresponding Cube template then ran a fresh guest with one vCPU,
1024 MiB RAM and a 10 GiB writable layer. A newly generated supervisor token
and Cube ingress credential authenticated the control path. The host attached
the authenticated reverse channel before the application started; this
fixture's broker refused all outbound dials. Direct egress remained deny-all.
Checks covered:

- React Pro, Marketplace and React Vite: HTML, transformed React entrypoint,
  and Vite client delivery.
- Next.js: server-rendered HTML with Next asset references.
- Express and FastAPI: successful root and health responses.
- Worker: a running supervised process and correctly absent web preview.
- All seven: pause/resume preserves supervisor boot time, process PID and
  restart count, followed by strict quiesced home-v2 and workspace-v2 exports
  with validated canonical digests.

All fixture guests were deleted. Images and templates remain reusable test
artifacts. These checks ran under shared functional-test load; they are not a
speed benchmark or a full browser interaction suite.

## Strict export caught startup-generated Next.js state

A fresh Next.js guest creates `.config/nextjs-nodejs/config.json`. The first
export correctly rejected the initial fixture manifest because that settings
path was unclassified. A disposable metadata-only inspection identified it;
no setting contents or credentials were recorded. The clean rerun explicitly
preserved `.config/nextjs-nodejs`, while leaving other `.config` paths
unclassified. No home-policy relaxation, blanket home copy, or image
permission change was needed. A later migration manifest must likewise
classify actual source/destination owner data; this baseline fixture manifest
is not a universal owner manifest.

## Reproduction and remaining gates

Build inputs, image checks, template creation/watch results and functional
logs are retained on the outer test host under
`/opt/baarcha-bench/cube-global-20260924/template-review/preset-matrix-20260924/`.
The same directory contains the fixture's `main.go`, `home.go` and `presets.go`;
the JSON pins their hashes and the executable hash. Orchestration scripts are
in the parent directory. Reports contain no tenant files, credentials or
signed URLs.

The prepared FastAPI image is now covered by an actual Cube template and
backend check, extending the earlier Docker-only FastAPI evidence. The
synthetic home import/config/reverse-copy proof remains documented separately;
this matrix exports each starter but does not repeat the complete import and
rollback sequence seven times.

Global migration attestations remain false. Actual owner-home manifests,
legacy preset assignments, application/native ABI and external-service
compatibility, per-guest disk budgets, production template deployment,
offline journal rollback/recreation, and API publish/remix/task lifecycle
acceptance must each be established by their corresponding checks. This
matrix does not substitute starter readiness for real-owner application
acceptance.
