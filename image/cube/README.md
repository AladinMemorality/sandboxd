# App templates for Cube

Build one credential-free image per runtime preset. Use an immutable reviewed
base identity that matches the source fleet's Node, Python and system libraries;
never export a running owner's container into a shared template.

```sh
docker build -f image/cube/Dockerfile \
  --build-arg BASE_IMAGE=sha256:REVIEWED_LOCAL_BASE_IMAGE_ID \
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
