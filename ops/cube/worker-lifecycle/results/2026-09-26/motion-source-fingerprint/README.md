# Fixed Motion review-source fingerprint

The five explicitly named Motion source files are non-executed review inputs.
Their existing app/server directories and files may be owned by root or service
UID985; parents above app remain root-owned. The narrow fingerprint reader walks
retained directory descriptors, refuses symlinks and writable modes, caps input
at1MiB and verifies file/layout identity after hashing. All helper/config paths
still use the unchanged root-only digest function.

All94 Linux-root tests passed, including real service-UID ownership, unreviewed
path, symlink, bound and concurrent-replacement regressions. No production source
ownership, routing, service or lifecycle changes were performed by these tests.
Maintenance helper SHA:
`83b2d3786bbcaba29e60cfc78f77aa78e3baecdf8175e789998d9a2789720945`.
