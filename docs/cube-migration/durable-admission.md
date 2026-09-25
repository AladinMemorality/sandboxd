# Durable active-allocation admission

The initial same-VPS profile supports **at most twelve active or uncertain
allocations**, each from an explicitly reviewed two-CPU/two-GiB template. This
is not a twelve-project storage limit. Paused apps retain their files and IDs;
their next wake may return retryable HTTP503 when all active slots are occupied.
It does not promise simultaneous execution of the current 61-project fleet.

The upstream worker's quota remains a second check. Its create scheduler uses
heartbeat-fed quota state, so serial HTTP requests alone cannot prevent bursts
from oversubscribing it. Sandboxd therefore reserves capacity in its own SQLite
transaction **before** each allocation. All production Cube management create,
resume, pause and delete operations must pass through the same guarded client
and database; direct management callers or provider automatic resume bypass
this bound and are prohibited for this deployment. Provider auto-resume is
rejected on guarded create. The unused raw memory-snapshot primitive is also
rejected under this bounded profile; publication continues using scoped source
archives. Existing runtime bindings without corresponding
admission records block guard initialization rather than being silently ignored.

## Reviewed configuration

Both sandboxd and the native offline CLI require `SANDBOXD_CUBE_ADMISSION` when
using Cube. Docker-only sandboxd startup is unchanged. Example shape:

```json
{
  "max_active": 12,
  "cpu_count": 2,
  "memory_mb": 2048,
  "templates": {
    "EXACT_REVIEWED_PRODUCTION_TEMPLATE_ID": {
      "cpu_count": 2,
      "memory_mb": 2048
    }
  }
}
```

Include all eight offered preset template IDs and retain contracts for older
in-use IDs until their VMs are removed. The template map is an operator-reviewed
immutable image/resource contract, not discovery of a template's contents.
Actual VM CPU/RAM/template metadata is checked after allocation and before a
connect. Unknown or nonuniform profiles are rejected. The code currently rejects
counts above twelve or profiles other than two CPUs/two GiB; future enlargement
needs a new reviewed capacity implementation, including measured worker overhead.
Changing the persisted count/profile is also refused at initialization.

This contract supplements migration's `--template-resources` map; both must
agree. It does not establish disk capacity, native compatibility, network
isolation or backup readiness. Reserve host/control-plane headroom independently.
Do not activate the profile until the reviewed worker overhead fits its budget.

## State and cancellation

Migration0032 adds `cube_admission_policy` and `cube_admission`. Each create is
keyed by the stable app ID, so an API retry that generates a different sandbox
ULID cannot create a second VM for the same pending app. A unique operation
marker travels in private provider metadata. Pending and active rows consume a
slot. A running connect reuses its slot. Deleting a paused VM temporarily reserves a
slot too: upstream destruction can resume it internally. If capacity is full,
that deletion waits for a retry rather than waking an unreserved VM. A confirmed
pause releases capacity but
keeps the app's creation fence; confirmed deletion permits a new app generation.

Every lifecycle mutation first acquires its durable generation via compare and
swap. Ordinary maintenance GET can release an automatically paused/deleted VM
only against the active generation read before that GET. An old paused response
cannot free a newer connect, and no observation releases a pending operation.

An already canceled request makes no provider mutation. Once the reservation is
committed, work uses a bounded context detached from the browser request (up to
90 seconds for create, 175 seconds for connect/pause/delete), finishes provider
verification and acknowledges SQLite. Browser disconnection therefore does not
by itself strand a slot. There are no automatic POST retries. A process crash,
transport ambiguity, invalid success response or failed acknowledgment retains
the reservation. It never expires by TTL. The API distinguishes capacity503
with `Retry-After` from pending-outcome409 requiring operator reconciliation.

Include both admission tables in consistent SQLite backups. Retain the same
policy and database during controller/image rollback. Restoring an older DB
without reconciling real provider allocations invalidates the bound.

## Explicit recovery

Read-only inspection does not stop the daemon and omits operation markers:

```sh
cube-migrate --database /DATA/state/sandboxd.db admission-status
```

For pending creation, locate the exact provider VM through its app/sandbox and
unique operation metadata. The offline adoption command verifies the pending
operation marker, exact template, app identity and actual resources before
binding the existing VM. It never issues a create request:

```sh
# Same reviewed Cube/admission environment and existing key/paths as sandboxd.
# Run under the enforced native-host offline maintenance fence.
cube-migrate --database /DATA/state/sandboxd.db \
  --adopt-runtime EXACT_EXISTING_VM admission-adopt
```

Migration's normal `adopt` command performs this admission step plus its existing
supervisor-secret and migration-metadata checks. Admission-only adoption does
not repair missing application bindings or supervisor credentials after an API
creation crash; those still need explicit ownership/credential recovery. A
pending create with no positively identified VM remains charged. Do not clear
it because a request timed out or one inventory read returned no match.

For pending connect, pause or delete of a known VM, first enforce offline
maintenance and **prove all prior provider requests have terminated**. Stop
admission sources, retain controller/request logs and provider lifecycle journal,
and match the failed operation/runtime to terminal worker results. If logs are
incomplete, use a reviewed provider maintenance/recovery procedure that fences
old request execution before verifying actual worker state. Elapsed time, a
browser disconnection or a single paused GET is not this proof. Do not set the
following flag until that evidence exists:

```sh
cube-migrate --database /DATA/state/sandboxd.db \
  --admission-key app:EXACT_APP_ID --provider-requests-drained admission-reconcile
```

The command rechecks the exclusive maintenance fence, obtains authoritative
runtime state, validates resource/template identity, and atomically reconciles
the pending generation as active, paused/released or deleted. It does not retry
the uncertain POST, delete data or repair unrelated allocations. Unresolved
operations stay charged. These commands have not been used on production.

## Validation scope

Real SQLite and localhost HTTP race fixtures cover concurrent independent
adapters, burst admission, already-running connect, restart after an ambiguous
create, changed sandbox IDs for the same pending app, browser cancellation,
automatic pause release, stale response fencing, ambiguous pause, template
size rejection, exact-token adoption and explicit fenced recovery. Fixture
provider behavior is synthetic; it is not a claim of live production burst
acceptance or a production disaster-recovery rehearsal.

## Disk preflight during serialized migration

Before each import, run the reviewed
[`check-cube-import-capacity.py`](../../scripts/check-cube-import-capacity.py)
as native root on the worker. It only reads mount information and `statvfs`;
it requires the dedicated `/data` XFS mount and at least 48 GiB available.
It accepts no threshold overrides. For example, over the existing reviewed SSH
transport, stream the script to `python3 -` on the worker, require exit zero,
and retain its JSON result immediately before executing that one import.
Do not run it in a container or against a different host filesystem.

Stop template builders and other imports while performing the serialized
maintenance migration. Check both measured compressed archive sizes and expanded
app/home sizes after export, plus the current destination baseline and guest's
own ten-GiB filesystem headroom. Repeat the native check before the next app.
The latest live scan's largest combined app+home was about 1.67 GB, suggesting
roughly 5 GB for old+staged+compressed data before filesystem/history margin.
This is only a preliminary estimate: the PG app's live socket prevented a full
home scan, and its exact budget must be recomputed after graceful database stop.

For normal creates, use the existing conservative worker disk-fill filter
against real physical capacity. The installed 65-percent threshold on the provisioned 320-GiB data volume
provides roughly 112 GiB at the last heartbeat. It is cached headroom, not an
atomic 48-GiB reservation: active tenant writes, pause snapshots and import
staging consume space afterward. The twelve-active limit bounds the number of
simultaneous VM RAM snapshots but does not cap tenant disk growth. Do not inflate
physical quotas or shrink owners' ten-GiB filesystems to hide this distinction.

Pinned upstream31d retains per-sandbox logical disk fields for paused objects,
but its scheduler disk filter compares filesystem occupancy percentages; it
does not reserve 61 times ten GiB from physical XFS capacity. Actual stored bytes
and transient copy/snapshot requirements therefore remain the acceptance input.

### App-specific HTTP services during offline migration

The online controller and offline migration broker use the same optional
`SANDBOXD_CUBE_APP_HTTP_SERVICES` operator JSON. Each entry is keyed by the exact
stable app ID and specifies a literal private IPv4 HTTP origin and a finite
GET/POST path list. Validation rejects protected management/worker addresses.
The offline broker selects only the frozen journal's `Source.AppID`; an owner’s
other projects and remixes receive no inherited permission. Changing the target
runtime, credentials, template, domain, or source app cancels the old channel and
its in-flight service requests. No model or bridge capability is added.

Classroom requires its separately reviewed service mapping and preserved
`workspace/classroom-library` sibling data. The host forwards the app's existing
Authorization only to the fixed reviewed origin; it injects no platform secret.
Generic private TCP, alternate ports, redirects, and arbitrary URL paths remain
blocked. Keep operator mappings with the independent configuration backup.

Historical twelve-character Docker container IDs are resolved by inspection and
pinned as full immutable IDs in the durable resource contract before source stop.
Resume and rollback reject a changed resolved identity; rollback does not require
the current target-template resource mapping to verify that pin.


### Serialized worker creation

Pinned upstream `31d911e` applies `creation_concurrent_num` using nonblocking
`TryAcquire` in `Cubelet/plugins/workflow/engine.go`. Its master buffer queue and
finite scheduling retries do not guarantee that a burst of provider requests
will wait safely for this one-workflow worker. The controller therefore queues
creates through a context-cancellable local gate **before reserving a slot**,
and holds that gate through provider acknowledgment and the durable completion.
Cancelled queued requests send no provider request and consume no reservation.

The SQLite transaction also rejects a second pending create across independent
clients and process restarts. That response is `ErrCreationBusy`, a capacity
error exposed as HTTP503 with the existing retry guidance. An unresolved retry
for the same app remains the explicit pending-operation response. A genuinely
ambiguous create holds the durable global creation fence until verified adoption
or operator recovery; it never expires merely because time passed. Existing
running-app Connect and other lifecycle operations retain their own admission
checks and do not wait on the create queue. No POST is automatically retried.

The opt-in real-worker burst fixture sends twelve concurrent client requests;
provider creation is serialized, and all twelve successful allocations remain
charged simultaneously. The fixture then exercises a rejected thirteenth
allocation and verified pause/replacement/resume accounting. Until its separate
live report exists, this describes the prepared test, not a completed acceptance.
