# Docker file API isolation fix

PUT previously used lexical path validation followed by pathname-based mkdir,
temporary writes, chown and rename. A tenant-controlled parent symlink could
redirect privileged writes. Content/list/export also resolved symlinks before
using pathname operations, leaving a replacement race.

The new helpers start at `/`, open every directory component with no-follow,
and retain those descriptors through each operation. This includes the mount,
`workspace`, and `app`, not only the requested relative path. PUT creates parents
and temporary files relative to retained descriptors, modifies ownership/mode
through the opened file, checks ancestor and temporary-entry identities, then
renames relative to the retained parent. Rename never follows the target leaf.
Content/list/export open descendants relative to retained parents and accept
only regular files or directories; content reads enforce the 2 MiB cap while
reading, including growth after open. Symlinks inside the app are also refused.

The raw-body, app-relative PUT contract, 25 MiB write cap, 0644 file mode and
workspace ownership remain. PUT replaces a file; it does not implement append.
Files and the containing directory are synced before successful return. A tenant
can still modify its own files concurrently; this is path-isolation protection,
not a transaction or snapshot of a running app. No deployment is included.

Tests cover mount/workspace/app/parent/leaf symlinks, FIFO rejection, parent and
temporary-file substitutions during body reads, outside content/mode canaries,
atomic replacement of a hardlink without modifying its other name, successful
nested writes/ownership, oversize rejection preserving the original, and a real
directory replacement during recursive descriptor-based reads. Existing read,
export, Cube delegation and the full API race suite also passed. The shared
native run includes another agent's Motion changes; validation.json identifies
its exact source archive and the owned file hashes. No test container, worker
mutation or tenant workload remains from this run.

## Tested internal-link compatibility follow-up

The final read/list behavior restores relative links contained within the app.
It opens the mount/workspace/app root with the same strict no-follow walk, then
uses Linux `openat2(RESOLVE_BENEATH | RESOLVE_NO_MAGICLINKS)` for the explicit
requested path. The kernel confines resolution atomically; absolute, escaping
and magic links fail. Unsupported kernels fail closed without a pathname
fallback. PUT remains no-follow, and recursive list/export still omit symlink
entries as they did before the fix. Internal file/directory links, escapes and
400 concurrent link substitutions were tested in the full nine-package native
race suite; see compatibility-validation.json for exact source/log hashes.
