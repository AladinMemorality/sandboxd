# Selected regular files from a restored recovery role

`select_role_members.py` is prepared/tested, not run on production artifacts.
Run only after full-object hash verification, independent GPG exit0 and strict
`cold_pair.py restore`. Keep the authenticated immutable `pair/` directory
unchanged; put selected files and later disposable clone disks in sibling
private directories beneath the same recovery generation.

Its root0600 plan contains exactly:

```json
{
  "version": 1,
  "capture_directory": "/PRIVATE-GENERATION/pair",
  "capture_manifest_sha256": "ACTUAL-AUTHENTICATED-MANIFEST-SHA",
  "selections": [
    {
      "role": "rollback",
      "member": "opt/baarcha-bench/cube-operator-recovery-live-20260925-01/journal.json",
      "output": "operator-journal.json",
      "sha256": "8dcee6fde79ed46c4eeb527c342fe9598c2ee1ac2ed77a620ddce759d219e3b0",
      "bytes": 14938
    }
  ]
}
```

Add the exact four original task-result receipts and four event receipts using
their reviewed source lengths/hashes. Source paths are literal POSIX tar-member
names, with the leading `/` stripped exactly as GNU tar recorded them. Unusual
Unix filenames, including backslashes/newlines, are literal; they are never
interpreted as destination paths. Only flat reviewed output basenames are used.
The separate controller key and SQLite are direct files in the pair and need no
nested tar extraction. Never put secret values in a committed plan.

```text
python3 select_role_members.py --plan /ROOT0600-PLAN.json \
  --output /ROOT0700-GENERATION/selected-NEW
```

The tool validates the pinned capture manifest, hashes each selected role before
and after reading, and rechecks the manifest before publishing. Selected members
must be unique regular nonsparse files with exact size/content hash. It never
uses `extract()`/`extractall()`, follows links, restores special modes/owners or
creates archive-named paths. Unselected owner symlinks/hardlinks are not followed
or materialized. Absolute/traversal member paths and duplicate selected entries
are refused. Each selected file is bounded32MiB, total256MiB,64selections and
2million archive headers. An actual fleet exceeding these bounds requires review;
no entries are silently omitted to make a proof pass.

New files are root0600 in a new root0700 directory, published atomically without
replacement after fsync. Failure retains private incomplete evidence. Success
records selected source associations and hashes, **not** application restore or
decryption success. Authentication provenance still comes from the independent
GPG/capture verification; supplying an arbitrary manifest hash is not that proof.
