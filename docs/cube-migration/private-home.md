# Reviewed private owner-home transport

App files and canonical task history already have separate migration channels.
A home manifest explicitly identifies additional owner files and workspace
siblings. It is a private, same-owner transport; never publish these archives or
use them to remix across owners. The migration coordinator must bind the
manifest and canonical archive digests to the durable owner/sandbox journal.

A version-one manifest contains disjoint relative paths and dispositions:

```json
{
  "version": 1,
  "entries": [
    { "path": ".runtimed", "disposition": "separate" },
    { "path": "workspace/app", "disposition": "separate" },
    { "path": "workspace/assets", "disposition": "preserve" },
    { "path": ".local/bin", "disposition": "preserve" },
    { "path": ".bashrc", "disposition": "preserve" },
    { "path": ".claude", "disposition": "retained", "reason": "Provider conversation stays on private source; destination starts fresh" }
  ]
}
```

This is an example, not a universal manifest. Every actual leaf and empty
directory must be classified. Parent directories of selected paths are structural
only; unknown siblings fail validation. Paths may not overlap, including a
nonadjacent sorted ancestor such as `foo`, `foo-bar`, `foo/x`.

- `preserve` copies an explicitly selected file or complete subtree. Modified
  shell defaults, Git configuration, tools, caches, data, and workspace siblings
  can be preserved. Known provider/auth/control paths are forbidden even when
  nested inside a selected parent. Selecting `.claude` wholesale is forbidden;
  an individually reviewed Markdown/text plan under `.claude/plans/` can be
  selected when other provider paths have their own disjoint retention entries.
- `stock` accepts only the exact SHA256 of reviewed `image/skel` shell/Git files
  and known empty `.gitkeep` files. Verified bytes are transported and restored;
  the destination's base-image defaults are never assumed equivalent. A changed
  stock file fails explicitly. Prefer `preserve` for owner-editable defaults
  when later customization must survive rollback under the same manifest.
- `retained` is limited to explicitly named provider/auth paths, with a recorded
  reason. Their bytes stay intact on the retained private runtime and are not
  in the home archive. An absent retained path at the new destination is valid.
  Do not describe provider conversations retained on Docker as migrated to Cube.
- `separate` is allowed only for `.runtimed` and `workspace/app`. The former is
  mandatory: canonical tasks use their dedicated transport and live tokens,
  sockets, locks, supervisor configuration, and migration staging never enter
  a home bundle. Home imports do not replace either separate channel.

`ValidateHomeManifest` reports preserved, stock, retained, and separate entry
counts plus preserved/retained bytes. Unknown paths, incompatible links,
credential scopes, and exceeded limits produce explicit blockers. A read-only
inventory does not prove consistency: validation repeats with owner processes
stopped, and the guest requires the authenticated quiescence fence.

The guest exposes `POST /export/private-home` with JSON manifest and
`PUT /import/private-home` with a ZIP body and base64url `X-Home-Manifest` header.
The runtime client streams requests/responses. Guest upload/export files are
0600 disk-backed temporary files under the separate runtime directory, not
whole-archive byte buffers. The raw manifest is bounded to 4096 bytes and 96
selectors. Archives are bounded to 4 GiB compressed, 8 GiB expanded, 1 GiB per
file, 500,000 entries, 4096-byte paths and 32 directory levels. ZIP central
metadata is bounded before allocating the archive index. Operators must reserve
space for recovery archives and old/new staged roots; these byte bounds do not
replace disk-capacity preflight or quotas.

Export uses descriptor-relative traversal without following symlinks. Normal
files are streamed. Multiply linked UID1000 files require a complete inode-link
inventory proving every link stays within this owner's home. Linux mount IDs
identify filesystem boundaries; overlay lower/upper files may report different
`st_dev` values on the same mount, which is not itself an escape. Actual mount
crossings still fail. Publication remains strict and does not use owner-link
exceptions. Relative home-contained links and absolute `/home/sandbox/` links
are preserved, along with the previously reviewed `.venv` Python executable-leaf
exception. Arbitrary absolute OS links, special files, and descendants beneath
symlinks remain unsupported.

Import validates every archive path, file length/CRC, stock hash and symlink
before modifying selected roots. Files and directories are staged and fsynced,
then each disjoint selected root is exchanged atomically. This is **not** one
atomic transaction across all roots. Interruption requires retry from the
journaled artifact before resume; retry replaces each selected root faithfully
and removes stale selected files. Crash staging stays under `.runtimed`, never
an implicitly ignored user directory. The coordinator retains the durable
archive and verifies export/import/export canonical digests before cutover.
The guest remains quiesced. Host-root rollback must restore UID1000 ownership
for selected roots without recursing through retained identities or `.runtimed`.

Local isolated validation covers stock-content changes, faithful modes/links,
unknown/credential paths, path traversal, source/destination symlinks, owner-only
hardlink flattening, retry after crash staging, dual credential transport,
redirect rejection, and an actual UID1000 authenticated guest roundtrip. A
160 MiB file fixture verifies streaming above the old 128 MiB per-file bound;
its zero-filled payload is not a production compression or throughput benchmark.
Production projects are eligible only after their own reviewed manifest,
workspace compatibility, disk bounds, and journaled roundtrip checks pass.
