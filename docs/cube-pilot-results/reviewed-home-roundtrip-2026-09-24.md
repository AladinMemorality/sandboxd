# Real Cube private-home roundtrip — 2026-09-24

The reviewed React v3 image passed actual authenticated Cube workspace-v2,
home-v2, config restart, pause/resume, and reverse-copy tests with synthetic
owner data. No production owner data was read, restored, modified, or migrated.
The exact results and fixture hashes are in [the JSON report](reviewed-home-roundtrip-2026-09-24.json).

Use template `tpl-ce9efc43b71248d9a0adfb90`, alias
`baarcha-react-reviewed-20260924-v3`, image
`sha256:6bad30fa19584d85f0dafc1680bca5851f1bb05f55a6abe0d6002165227253d0`.
It retains the earlier template's one CPU, 1024 MiB RAM, 10 GiB writable layer,
credential-free bootstrap, scoped supervisor credentials and direct deny-all
network policy. The fixture's reverse broker refused every outbound dial.

## Image correction caught by the actual transport

The reviewed production base contained build-only pnpm store index links under
`/home/sandbox/.cache/pnpm-store/v10/projects` pointing outside owner home to
five `/opt/templates` directories. A real home-v2 export rejected them with
HTTP 422; the same image's UID 1000 filesystem validator independently reproduced
that rejection. Transport policy was not weakened.

`image/cube/Dockerfile` now removes that known build-only store after preseed,
keeps `.cache` owned by sandbox, and restores `USER sandbox`. No tenant data is
involved in this image-build operation. All 986 links in the prepared React app
resolved inside its own tree afterward. The actual image's home export passed,
and the React starter still reached readiness in 832 ms with Docker networking
disabled. The v3 fixture image applies the exact cleanup step atop the previously
reviewed v2 image; future canonical builds include it directly.

## Actual guest roundtrip

A fresh v3 Cube guest started its reviewed app through an authenticated reverse
channel. The fixture quiesced it, exported home-v2, and installed that baseline
into a synthetic retained-source directory. It added:

- Workspace source, private `.env`, `.git` bytes and runtime data.
- Non-workspace home data and a workspace sibling directory.
- A Unicode filename, a relative link, a reviewed Python interpreter link and
  the literal-backslash systemd package filename supported by manifest v2.
- Distinct source-only provider/control sentinels, explicitly retained rather
  than transported.

The private workspace import restarted the supervisor. Home import succeeded
twice, verifying retry behavior. Guest exports then matched the source's exact
canonical content, mode, path and link digests. A runtime config update caused
a second supervisor restart; the existing binding still authenticated, and the
synthetic app observed its configured value. Pause/resume preserved the config
revision, supervisor boot and frontend PID.

The running synthetic app wrote **new** workspace and home files inside Cube.
After quiescence, fresh exports were reverse-copied into the retained synthetic
source. Re-exports matched both new guest digests, and source provider/control
sentinels remained unchanged. Thus the reverse-copy check includes post-cutover
writes, not merely the original archive. The small final archives were 1421
workspace bytes and 4656 home bytes; this run is not a large-data capacity test.
The guest and synthetic source directory were removed after success.

## FastAPI image check

A separate final image was built from the same pinned production base and
current supervisor, with FastAPI's virtual environment prepared during build
and the same pnpm-cache cleanup. Under UID 1000 with Docker networking disabled,
its declared `/health` endpoint returned successfully in 421 ms. Image identity
and Python/FastAPI/uvicorn/watchfiles versions are recorded in the JSON. This
validates the prepared image's offline starter; no FastAPI Cube template or
production Python application was tested in this run. The later
[seven-preset matrix](reviewed-preset-matrix-2026-09-24.md) adds actual Cube
FastAPI startup, pause/resume and private export checks.

## Evidence and limits

Exact fixture source/binary and sanitized reports remain on the outer test host:
`/opt/baarcha-bench/cube-global-20260924/template-review/cube-reviewed-app-image/`:
`home-main.go`, `home.go`, `reviewed-home-pilot`, `home-report-v3.json`, and
`Dockerfile.cache-clean`. FastAPI build inputs, logs, versions and readiness are
under the sibling `cube-reviewed-fastapi/` directory. The nested VM has the
same home fixture and reusable template, with no owned running guest left.

This proves these real transport and reverse-copy operations. It does **not**
prove the offline migration journal's complete Docker recreation/rollback path,
real-owner fleet ABI, maximum archive or guest disk capacity, project publish
and remix, model relay, or every application's external-service compatibility.
Those app-runtime acceptance gates remain separate. Screenshot workers remain
an independent platform service and are not a requirement for app migration.
