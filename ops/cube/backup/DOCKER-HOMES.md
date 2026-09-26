# Fenced Docker-home freeze and restoration candidate

Source-only preparation. No customer container was stopped or restarted to test
this helper. Fourteen local tests cover the journal state machine, real flock
exclusion, source/definition/policy drift, timeout/ambiguous acknowledgements,
active tasks, stopped controller/direct-writer checks and overlapping writable
container mounts. Native production execution remains a separately reviewed
maintenance operation, not an implicit prerequisite action of this document.

Stage these exact reviewed files together: `docker_homes.py`, `capture_roles.py`
and `cold_pair.py`. Use the same freshly reviewed root0600 roles config consumed
by `capture_roles.py`, with the complete canonical Docker source inventory and
full immutable container IDs. New projects invalidate an old plan. The config
hash must match its actual bytes; controller/config/Caddy hashes must describe
the actual closed maintenance state, not future expected booleans.

The parent maintenance coordinator must continuously own these four exclusive
open-file-description locks and pass those descriptors using `pass_fds`:

1. `/opt/baarcha/deploy-release.lock`
2. `/opt/sandboxd/deploy-state/deploy.lock`
3. `/run/lock/cube-operator-acceptance.lock`
4. `/opt/baarcha-bench/cube-workload-operator.lock`

The helper verifies an independent shared lock attempt fails before checking
that its inherited descriptor is the same exclusive OFD. It does not acquire a
missing lock or close/unlock the parent's descriptors. The parent must keep its
locks and routing fence on helper failure, timeout or lost acknowledgement.

Before and throughout mutations it verifies:

- the exact reviewed offline Caddy JSON plus actual HTTPS503 write/preview routes
  and a working landing page;
- the exact stopped controller ID/image/environment with restart policy `no`,
  closed canonical SQLite and no active task, pending allocation/recovery or
  incomplete migration;
- all reviewed direct writer units inactive, complete unchanged Docker home
  mapping, and no other running container with an overlapping writable archive
  source mount.

These observations cover the enumerated cooperating writers. They do not prove
that an arbitrary root process cannot write host files. The operator handoff and
held deployment locks remain necessary. The helper never stops platform
PostgreSQL, inference, unrelated services or unrelated containers.

From the held parent, execute each child with the same four inherited FDs:

```text
python3 /REVIEWED/docker_homes.py freeze \
  --config /ROOT0600/roles-config.json --config-sha256 EXACT-FILE-SHA256 \
  --journal /ROOT0700-PARENT/NEW-FREEZE-JOURNAL \
  --inherited-lock-fds FD1,FD2,FD3,FD4 --execute
```

Freeze durably records exact original running states, restart policies and
immutable definition hashes (never environment values). It first sets **every**
selected source to restart=`no`, including previously stopped `always` sources,
so a Docker daemon restart cannot silently wake them during capture. Each
request is fsynced before its effect and acknowledged only after authoritative
inspection. It then stops only previously running exact IDs with
`docker stop --time=-1`. A 180-second client bound may terminate the waiting CLI;
it never escalates Docker's stop to SIGKILL or retries. Such a timeout is an
ambiguous stop and blocks automatic restoration, even if a later inspection
finds the container stopped. Retain all evidence for explicit operator review.

On success `frozen.json` proves all selected containers stopped with restart=no.
The helper's separate SQLite maintenance OFD closes when the command exits,
allowing `capture_roles.py` to acquire its own OFD; the parent's four locks stay
held. MyHomeTroc PostgreSQL18 clean-control-state/natural socket-removal checks
remain in `capture_roles.py`. A frozen container alone does **not** prove clean
PostgreSQL shutdown or justify omitting its socket/WAL.

Run role capture and close the cold pair while sources remain stopped. Once all
required archives are finalized, restore Docker sources **before** the boot
transition recreates the controller or changes reviewed config pins:

```text
python3 /REVIEWED/docker_homes.py restore \
  --config /ROOT0600/roles-config.json --config-sha256 EXACT-FILE-SHA256 \
  --journal /ROOT0700-PARENT/NEW-FREEZE-JOURNAL \
  --inherited-lock-fds FD1,FD2,FD3,FD4 --execute
```

Restore requires the complete acknowledged freeze, unchanged baseline/config,
all sources still stopped and all policies still `no`. It starts only exact IDs
that were originally running. Only after those starts does it restore **all**
original restart policies, including the policies of previously stopped sources
without starting them. Every step rechecks all already-processed identities and
policies. It never recreates, rewinds, removes or prunes data/containers/images.
`restored.json` establishes process-state/policy restoration, not application
HTTP/SQL readiness; the parent performs subsequent application validation and
restores traffic only after its complete recovery sequence succeeds.

Any refused/incomplete freeze or attempted partial restore prevents replay.
There is no generic reset flag or automatic inverse action: preserve the private
journal, inspect exact affected IDs, and use a separately reviewed reconciliation
under the existing parent fence. A vanished parent or lost lock requires a new
actual maintenance fence, not treating the old journal as current authority.
