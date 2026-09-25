# One-app controller enrollment candidate

`prepare.py` writes a new root0700 directory containing private Compose
overrides, their fully resolved configuration and a sanitized hash manifest.
It does not install files, contact guest APIs, start services, build images or
recreate containers. It takes the four existing deployment/operator locks while
checking the current controller and rendering the candidate.

The root0600 `--decision` document names the exact synthetic app and its external
owner/project identities. The renderer checks that canonical SQLite row has
the `node-postgres` preset and no sandbox. The decision must explicitly approve
only this canary, reference the reviewed worker/network evidence, and forbid
global rollout. Prior incomplete isolation reports are not rewritten.

```sh
python3 prepare.py --decision /ROOT-PRIVATE/scoped-decision.json \
  --out /ROOT-PRIVATE/controller-candidate-NEW
```

Outputs `runtime-compose.json` and `active-images.json` must be reviewed together:
the active-image override is loaded last and previously contains false Cube
flags. The renderer preserves unrelated settings and pins the current immutable
controller image. Both management sidecars use the reviewed immutable image,
share the controller network namespace and publish no ports. Only the controller
gets the read-only storage observation directory. All eight preset keys match
`preset.go`; NIC egress remains deny-all and no app-specific HTTP exception is
granted. Fresh DNS and host IPv4 observations only add protected destinations.
Native IPv6 remains denied; the reverse broker is IPv4-only.

`https://cube-model.baarcha.tn` is a protected logical origin, not a claimed
public TLS route. With reverse egress enabled, model and bridge calls use the
guest-local broker and fixed authenticated host callbacks. Global credentials
remain in the existing private controller proxy. Direct relay mode is not
authorized by this canary.

The root coordinator owns actual drain, atomic configuration installation,
single controller recreation with both relays, identity refresh and rollback.
Do not run an ordinary release during this temporary canary: the release script
currently requires global configuration when Cube is enabled, and its Docker
mode does not reconnect pre-staged relay namespaces. The new schema34
coordinators must remain installed once a canonical storage policy is enrolled.
Disabling Cube after a binding exists is not a provider rollback.

Tests cover scope/ownership rejection, all eight real preset IDs, preservation
of unrelated overrides and the read-only mount/relay contract. Rendering proves
configuration shape; real authenticated transport, fixture execution, restart
and restore remain separate acceptance steps.
