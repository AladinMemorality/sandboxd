# Source-preserving project cohorts

`cohort.py` wraps the existing native `cube-migrate` state machine with the
production traffic/task drain and operation locks. It leaves the Cube worker
running, keeps the current compatible controller and never retires a source.
The final Cube-only controller cutover remains a separate step after all projects
are migrated.

Prepare a fresh private stage containing this module, its sibling lifecycle
modules, schema migrations and `cohort.PRIVATE.json` next to the module. Its
configuration pins the reviewed CLI, home manifests, explicit preset map,
template resources, source container generations and fleet identity. Native
inspection resolves legacy twelve-character database container IDs to exact
sixty-four-character Docker identities without rewriting the database.

The initial runner accepts one to four cleanly stopped, nonrestartable ordinary
projects. It refuses Motion, PostgreSQL, active sources and existing migration
journals; those require their own acceptance or explicit recovery. It does not
silently skip them or claim that this restriction completes a global rollout.

The maintenance plan uses the existing exact worker/controller/routing identities
and pins all coordinator source files. Run `--check` in a fresh journal directory
first. It acquires the actual locks and rechecks the deployed state, but changes
no service. `--execute` repeats those checks, drains traffic and work, stops the
controller, revalidates the fleet and runs migrations sequentially. It stores
private per-project archives on mounted NVMe and a diagnostic SQLite checkpoint;
the checkpoint is never restored as a data rollback.

Only a completed migration journal with matching target identity, app/home
archive hashes and canonical Cube binding can advance the expected binding set.
The runner then starts the same controller and reconstructs its management
relays, verifies canonical bindings/storage readiness, and checks each project's
normal authenticated HTML, JavaScript entry modules, files and task history
through the local preview handler while public traffic remains fenced. Each
verified project is returned to its original paused state through the normal
task-aware controller API. It then restores the complete original routes and
timer states. A failed phase retains the maintenance locks
and journal for explicit inspection. Do not restart an ambiguous migration or
flip its provider back to stale Docker data. Use the CLI's journaled adoption,
resume or rollback workflow as appropriate to its actual recorded phase.

The CLI verifies content, configuration and guest readiness before provider
commit. `--stop-after-import` explicitly stops at the durable, quiesced `imported`
phase while Docker remains authoritative. The coordinator uses that boundary to
reconcile the known template-only `.bash_logout` when the source has no such
path. The helper accepts only the reviewed220-byte stock file with exact content,
type, owner, mode and single-link identity. Modified files and links are retained
and refused. It records the stock bytes before removal, then resumes the same
runtime and journal for the full verification. It does not weaken manifests or
exclude future owner edits from rollback. The flag refuses unrelated actions
and journals already beyond the requested boundary.

Retained originals and per-project archives are migration recovery
material; neither a cohort nor the online NVMe copy proves a full off-host
disaster-recovery restore.
