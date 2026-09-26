# Online pre-copy and frozen delta

The first full-role capture spent too long reading millions of small source
files with production fenced. `warm_mirror.py` reuses a private online rsync
copy and limits the final content transfer to files that cannot be proved
unchanged. It performs no service lifecycle operations and never calls a live
cache an accepted backup.

The online copy begins in an empty root0700 directory. It records complete
source generations before and after rsync, including device, inode, ctime,
mtime, size, mode, ownership and symlink targets. Rsync transfers literal links,
hardlinks, numeric IDs, ACLs and xattrs. Its transfer checksums establish copied
content; its default size/mtime quick check is not the final acceptance test.

After the copy process exits, `prepare --cache PATH` imports both source scans
into a private SQLite index and hashes the cached regular data. It refuses
changed manifests, duplicate or escaping paths and special cached files. Its
receipt pins the index and source scope. Preparation only reads the completed
cache. `estimate` compares it with current sources while production remains
online; live sockets are counted without treating that observation as closure.

The production window requires this prepared receipt and a successful online
estimate before draining. It separately rechecks the exact canonical inventory,
images, paths, source processes and original states. Default final delta limits
are 512 MiB and 50,000 files; exceeding them refuses before modifying the cache.
The source and space ceilings remain 5 million entries and 64 GiB. These bounds
do not change worker capacity or guest admission.

Only `capture_roles` calls `seal`, after the existing actual writer fence and
clean PostgreSQL control check. It supplies the same inherited operation locks
and repeatedly rechecks stopped sources, controller, routes and direct writers.
A regular cache entry is reusable only when the source generation agrees
before/after the initial copy and now, its online ancestor directories were
stable, its cached metadata matches the source and the cache generation still
matches the hashed entry. This catches same-size/same-mtime edits, inode
replacement, cache corruption and directory moves during the online copy.

Untrusted cached regular files are removed through no-follow directory file
descriptors, forcing content transfer. A complete rsync pass reconciles paths,
deletions, types, hardlinks and metadata; a dry run then requires zero remaining
changes. Only exact reviewed socket inodes can be omitted, using literal paths
without pattern characters. A second complete source scan must match the first
frozen scan, and the live fence must still pass. The generation is single-use.
Failure retains evidence and cannot publish a seal.

The role archiver reads that private mirror while keeping the original absolute
source paths as relative tar members. It preserves numeric ownership, links,
ACLs and xattrs; neither mirror prefixes nor operator directories enter restored
project paths. Original cold-pair, archive hashes, disk comparison, encryption,
off-host readback and isolated application/SQL restoration gates still apply.

The copy and seal are operator-owned private generations. No other process may
write the cache during preparation, sealing or archiving. Historical receipts
and existing output directories cannot be reused. Do not install or invoke an
old consumed window configuration simply because this helper exists.
