# App templates for Cube

Build one credential-free image per runtime preset. Use an immutable reviewed
base identity that matches the source fleet's Node, Python and system libraries;
never export a running owner's container into a shared template.

```sh
docker build -f image/cube/Dockerfile \
  --build-arg BASE_IMAGE=REVIEWED_BASE_REPOSITORY:REVIEWED_TAG \
  --build-arg RUNTIME_PRESET=react-pro \
  --build-arg CUBE_REVERSE_EGRESS=1 \
  -t baarcha-cube-react-pro:REVIEWED_REVISION .
```

Repeat for `marketplace`, `react-vite`, `nextjs`, `node-express`, `fastapi` and
`worker`. The shared Go preset registry selects the starter and manifest. The
build refuses an unknown preset, a linked app directory or any existing app
files. Starter dependencies are copied during the image build so a fresh guest
does not repeat that work. FastAPI's starter environment is installed at build
time; record its resolved package versions with the image review. The build
does not start a tenant app or supply runtime credentials.

`CUBE_REVERSE_EGRESS` defaults to `0`. Setting it to `1` prepares the fixed local
proxy for a separately configured host broker; it does not authorize global
admission or relax the guest's network policy. The bootstrap still accepts only
the fresh supervisor address and token at `/init`.

Register the resulting images with Cube using bootstrap readiness port `49983`.
Record the actual template IDs and image digests only after successful builds
and guest acceptance. Validate every preset's preview/worker, cold create and
snapshot restore. Then validate imported owner workspaces, native dependencies,
model/bridge calls and rollback separately. A Docker build is not evidence that
the same image boots correctly under Cube.

The source fleet inspected on 2026-09-24 uses local base image
`sha256:9ec445151344a7baa295b085bebe1a0352ab930601fe0d9ed2bf1283bbaa6678`:
Node 22.23.2, Python 3.13.5, pnpm 10.34.4 and glibc 2.41. This records the inspected
candidate's identity, not a completed template or migration approval.

Before building, resolve the selected base tag with `docker image inspect` and
compare its `.Id` with the reviewed identity. Do not rely on a mutable tag alone.
The reverse HTTP proxy profile asserts Node 22.21 or later in the 22.x line,
which supplies native `fetch` and default HTTP/HTTPS agent proxy support.

Six source-base starter fixtures passed with networking disabled and UID 1000:
React Pro 730 ms, marketplace 631 ms, React/Vite 629 ms, Next.js 4679 ms and
Express 116 ms to their first successful HTTP response; the worker stayed alive
for its two-second observation. These times exclude image creation and starter
copying, and are Docker checks rather than Cube restore measurements. The
FastAPI environment still requires a built-image check. The fixture's first
attempts failed because its temporary workspace was not writable/executable;
the successful run used a dedicated writable, executable sandbox-owned tmpfs.

An OS-link review found both required Python interpreter destinations in this
base. Twenty-three optional font/X11/systemd package targets are absent, so the
literal owner-link contracts alone cannot establish those tools' compatibility.
Preserve the source links and verify the actual owner tools after import.
