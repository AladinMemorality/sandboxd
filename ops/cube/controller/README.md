# Cube controller deployment profile

`deployment_profile.py` prepares the three existing Compose layers for the
separate Cube-only executable. It does not apply them or stop any service.
The existing `sandboxd` Compose service/network identity is retained for the
management relays and lifecycle coordinators; the image contains neither the
legacy executable nor a Docker CLI, and the rendered service has no Docker
socket mount.

Supply the current base, runtime and active-image files, an immutable reviewed
image digest, the exact Motion app ID and a new private output directory:

```sh
python3 deployment_profile.py --base /PRIVATE/base.yaml \
  --runtime /PRIVATE/runtime.json --active /PRIVATE/active.json \
  --image sha256:REVIEWED_DIGEST --motion-app REVIEWED_APP_ULID \
  --output /PRIVATE/new-candidate
```

Render all three candidate layers with the deployed Compose version, project
directory and environment. Call `validate_resolved` on the resulting JSON and
compare the retained environment with the actual deployed controller without
printing credentials. The helper records source hashes for a later freshness
check. Candidate files and resolved output contain private configuration.

The profile keeps historical files read-only, grants writes only to state,
agent authentication, library and logs, and mounts the storage observer and
Motion socket read-only. Existing ports, routes, relay namespaces, credentials
and admission policy remain in the source layers. New projects use Cube.

Apply only after every customer binding has been migrated and application
acceptance has passed, under the existing deployment and maintenance locks.
Refresh source hashes, fleet state and service identities before cutover.
The Cube-only executable deliberately refuses a mixed Docker/Cube fleet.
Preserve the original layers and image for an explicitly reviewed recovery;
restoring an old provider row alone does not restore current customer data.

The 2026-09-26 evidence includes eight profile tests, a real three-layer Compose
2.40.3 render, retained-environment comparison, lifecycle compatibility, and a
smoke test of the actual image without network access or host mounts. These
checks prepare the cutover; they do not establish production deployment.
See [the result](results/2026-09-26-deployment-profile/result.json).
