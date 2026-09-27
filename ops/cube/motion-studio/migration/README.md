# Motion customer cutover

This coordinator migrates only Motion app `01M3CKN983PFRGMD711PCEPDFD`, sandbox
`01M3CKN99ZF90BEEA4DS66YAQV`, using the live-accepted template
`tpl-c0c9813b42db46898f7ddd9f`. Ordinary cohort validation continues to reject
Motion. The dedicated coordinator requires the pinned real guest/worker,
upload/media, isolation and fixture-cleanup receipts before it can proceed.

The native CLI retains full source/home/archive/history validation and exclusive
database ownership. Its Motion readiness gate reads worker status and projects
through the imported app before committing the provider. The operator changes
the CLI's process-local `react-vite` template selection only; the global preset
map remains unchanged. The encrypted original worker URL/key and APP_ORIGIN
are preserved for the retained Docker source. No worker film/media mutation or
paid media task is part of this migration.

Once the journaled customer migration completes, the existing Motion-capable
controller image is recreated with three narrowly reviewed changes: exact app
selection, admission of that exact template under the same four-slot resource
contract, and a read-only bind of `/run/baarcha-motion-studio` with host-path
creation disabled. All three actual Compose layers are rendered before mutation;
the complete result must differ only in those changes and pinning the same image
by immutable digest. Other services, routes, environment, mounts, templates and
credentials must match. The runtime and active layers plus the worker-stop
admission policy change together under the existing release locks. Original
configuration bytes are privately journaled before writes.

There is one recorded controller-recreation attempt. An ambiguous result holds
the maintenance fence for inspection rather than replaying Compose. The new
controller must pass exact image/environment/mount/relay/readiness checks before
its new ID is written to the worker-stop contract. The original Motion Docker
source stays stopped after commit. Normal authenticated application APIs then
verify stable preview routing, entry modules, source files and historical task
results. Motion additionally must read the actual shared worker and existing
film count through that production preview; the worker's complete project JSON
fingerprint remains unchanged. Only then are the complete online routes restored.

If native migration fails before commit and its journal is safely aborted, the
unchanged original controller can be restored. Unknown allocations, unfinished
journals, changed source identities and uncertain replacement outcomes remain
fenced. Do not externally open the canonical SQLite database while the native
CLI process runs. Observe the unit and numbered operation receipts instead.

This transitional step does not replace sandboxd or enable the global Cube
default. The two remaining Minecraft/Playit projects still require their existing
game tunnels; final Cube-only controller replacement follows their migration.
