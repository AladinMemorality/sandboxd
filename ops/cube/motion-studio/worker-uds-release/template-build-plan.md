# One new Motion-capable template — commands prepared, not executed

Source pin: `5fdb953dea1aafaf486b5e3fdd63e20de2781a89`, matching the controller
source root deployed at 22:15 UTC. Use an immutable archive of that commit, not
the current shared dirty worktree. The source advertises `motion-worker-v1` only
when both Cube and reverse-egress guest modes are enabled. Binary strings or the
image tag are not the final authenticated capability proof.

Read-only inspection confirmed both required amd64 images and Go cache volumes
already exist on the outer VPS:

- Credential-free production ABI base:
  `sha256:9ec445151344a7baa295b085bebe1a0352ab930601fe0d9ed2bf1283bbaa6678`
- Go builder:
  `sha256:3d699e4d15d0f8f13c9195c0632a16702b8cbdece2955af1c23b37ae5d55a253`
- `baarcha-cube-go-mod` and `baarcha-cube-go-build` caches.

No existing preset image, owner container or guest disk is a build input.
`Dockerfile.template` mirrors the final prepared stages of the pinned
`image/cube/Dockerfile`, using three separately compiled binaries so the Go build
can use the already reviewed, bounded, network-none build container. It prepares
only react-vite and removes the build-only external-link pnpm store. No package
installation or external download is part of this plan.

## Build on outer host after root's resource window

Proposed new private stage:
`/opt/baarcha-bench/cube-motion-template-5f-20260925-01`.
Proposed local image tag: `baarcha-cube-motion-react-vite:5fdb953-20260925-01`.
Create the stage exclusively and refuse any existing image tag/stage rather than
overwriting. Extract the exact `git archive 5fdb953... control-plane` source,
record its archive/file hashes, and place the reviewed Dockerfile in a separate
`image/` directory. The source may not contain `.env`, owner data or credentials.

The concrete bounded compilation, with stage path expanded explicitly, is:

```sh
docker run --rm --name cube-motion-template-build-20260925-01 \
  --network none --cpus 2 --memory 2g --memory-swap 2g --pids-limit 256 \
  --tmpfs /tmp:rw,exec,size=512m -e TMPDIR=/tmp -e GOMAXPROCS=2 \
  -v baarcha-cube-go-mod:/go/pkg/mod \
  -v baarcha-cube-go-build:/root/.cache/go-build \
  -v /opt/baarcha-bench/cube-motion-template-5f-20260925-01/source/control-plane:/src:ro \
  -v /opt/baarcha-bench/cube-motion-template-5f-20260925-01/image:/out \
  -w /src \
  sha256:3d699e4d15d0f8f13c9195c0632a16702b8cbdece2955af1c23b37ae5d55a253 \
  sh -ec 'CGO_ENABLED=0 go build -trimpath -o /out/runtimed ./cmd/runtimed
          CGO_ENABLED=0 go build -trimpath -o /out/cube-init ./cmd/cube-init
          CGO_ENABLED=0 go build -trimpath -o /out/cube-template ./cmd/cube-template'
```

Use the existing transient command deadline (600 seconds) around the build,
record exit status and exact container removal, and hash all three executables.
No live sockets or production directories are mounted. A missing cached module
is a build failure, not permission to add network access automatically.

The prepared image build uses Docker's bounded legacy build container options;
fail if this reviewed backend is unavailable rather than silently replacing it
with an unbounded daemon build:

```sh
DOCKER_BUILDKIT=0 docker build --pull=false --network none \
  --cpu-period 100000 --cpu-quota 200000 --memory 2g --memory-swap 2g \
  --file /opt/baarcha-bench/cube-motion-template-5f-20260925-01/image/Dockerfile \
  --tag baarcha-cube-motion-react-vite:5fdb953-20260925-01 \
  /opt/baarcha-bench/cube-motion-template-5f-20260925-01/image
```

Record image ID and re-extract the three binary hashes from a stopped, owned
inspection container, then remove only that inspection container. Inspect image
configuration privately to verify UID sandbox, expected bootstrap command and
credential-free environment; no full inspect/env output in shared logs.

## Transfer to the actual worker registry

Use the existing private nested SSH transport (operator-key, port20222 and pinned
known_hosts), not the retired benchmark VM on19222. `docker save` the exact new
tag to a new stage file, hash it, copy through that SSH path and verify the hash
before `docker load`. Keep this operation inside the root-coordinated build
window and require enough source/target free space for both tar and image.

Inside the worker, use only the newly imported image:

```sh
docker tag baarcha-cube-motion-react-vite:5fdb953-20260925-01 \
  127.0.0.1:5000/baarcha/motion-react-vite:5fdb953-20260925-01
docker push 127.0.0.1:5000/baarcha/motion-react-vite:5fdb953-20260925-01
```

Resolve and record its **actual registry RepoDigest**, together with the local
image ID and archive/binary hashes. They are different identities; do not turn
the image ID into an invented `@sha256` manifest digest. Require exactly one
matching repository digest. Existing eight image tags/template records and the
retained failed template are unchanged.

## Register one fixed alias with a private intent/job journal

Root first verifies the exact expected owned-canary inventory and no unrelated
active task/template build, reserves one 2-CPU/2-GiB template-build slot under
the reviewed operator locks, and checks native/storage headroom. The canary
need not be deleted; any pause is a separate root-coordinated action. Do not
run the existing eight-template loop, which assumes an empty worker.

Use a new private worker directory
`/root/cube-production/motion-template-5f-20260925-01`. Before dispatch, create
and fsync an exclusive `create-intent.json` containing the pinned image digest,
alias, expected baseline inventory, exact arguments and timestamp. Refuse an
existing intent, job file or alias; an uncertain response is inspected by alias
and job metadata, never replayed automatically.

The one create invocation uses the exact production registration resource and
deny-all options (substitute only the resolved registry digest):

```sh
cubemastercli tpl create-from-image \
  --image '127.0.0.1:5000/baarcha/motion-react-vite@sha256:ACTUAL_MANIFEST_DIGEST' \
  --alias baarcha-motion-react-vite-5f-20260925-01 \
  --expose-port 3000 --expose-port 3001 --expose-port 3031 \
  --probe 49983 --probe-path /health \
  --cpu 2000 --memory 2048 --writable-layer-size 10Gi \
  --with-cube-ca=false --deny-out-cidr 0.0.0.0/0 --json --detach
```

Immediately fsync the private response/job ID. Then use the supported
`cubemastercli tpl watch --job-id EXACT_JOB_ID --json` with a 900-second bound,
and `tpl status --job-id EXACT_JOB_ID --json`. Persist their outputs and require
READY plus stored resource/network/probe values matching the intent. A failed
or unfinished job is retained for review. Do not edit the eight-preset manifest,
global preset mapping or old alias as part of this single candidate build.

## Required capability and adapter acceptance

Use the existing scoped owned-guest acceptance/control-plane flow with the new
exact template ID and a fresh authenticated runtime generation. Read authenticated
supervisor status and require `capabilities` contains `motion-worker-v1`;
`http://127.0.0.1:49983/health` alone cannot establish it. The current controller
and offline migration code reject the URL overlay without this capability and
acknowledge its config-revision marker only after successful application.

Then complete the existing README's owned-film route/50-MiB upload/range/revocation
cases through the actual reverse channel and dedicated Unix worker. Coordinate
the exact app mapping for the synthetic acceptance fixture; it must not authorize
the real Motion app or siblings implicitly. The dedicated worker's current TCP
listener/data remain intact throughout. Final actual Motion migration still uses
its journal, original encrypted URL/key and preserved external data backup; no
customer frontend installer or paid media generation is required to prove these
transport paths.
