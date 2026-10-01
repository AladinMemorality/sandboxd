# MyHomeTroc recovery, September 29

This is an exact-incident operator, not a general recovery command. It binds the
known stable app/sandbox, original runtime, reviewed template and private VPS
stage in `main.go`. Do not substitute another customer's IDs or rerun `run`
after a journal has been created. Ambiguous creates require explicit adoption.

The original current disk was independently reflinked while its native task was
absent and no process held its inode. A dedicated rescue VM repaired only a
scratch copy, mounted the merged filesystem read-only with networking disabled,
and exported the complete home. The original disk, capture and full tar remain
private. No whole-worker reboot was claimed.

The bounded tar validator accepted 34,903 entries and no external symlinks.
Conversion preserves every reviewed home root, transports `workspace/app`
separately and keeps `.runtimed` identity separate. The only removed data in the
derived import is the exact stale regular PostgreSQL `postmaster.pid`; its hash
and the unchanged full source tar are retained. `workspace/.gitkeep` is included
under the existing stock-file contract. Customer files are never host-extracted.

Build this Go source as a command inside the control-plane module (CGO enabled
for SQLite). `validate` invokes the canonical Go workspace/home/interpreter
validators. `preflight` opens a disposable controller DB clone using the real
pinned worker policy, encryption key and frozen app config. It cannot create a
guest or change the production binding.

`run` holds the deployment and worker locks. `maintenance.py` checks for active
AI tasks and pending creates, disables automatic controller restarts, stops the
exact retained container, and takes a consistent SQLite/key backup. The Go
operator rechecks the original disk hash and open handles, then uses the existing
offline recovery journal for one create, private imports, canonical re-export
comparison, frozen config application and a read-only PostgreSQL health query.
Only then does it commit the stable binding. Every failure preserves the source.
An unfinished journal keeps the controller fenced; a pre-journal failure or a
completed commit restores the original container and restart policy.

Do not query even read-only SQLite while this operator runs: the exclusive
maintenance check deliberately rejects other processes with the DB open. Monitor
only `operator.log`, `operator-progress.json` and the systemd unit. The first
attempt stopped safely before creating a journal after a diagnostic reader was
observed; the controller was restored automatically.

Credentials, customer archives, config, database copies and runtime tokens must
stay under the private VPS incident directory, never this repository.

The initial create was conclusively rejected by CubeMaster request
`229e031d-4777-4c50-aec5-c68037568bc5` with terminal code 130597 (`no more resource`)
before allocation. Its log binds the exact journal operation and both workers'
inventory contains no matching target. The cause was the native scheduler's
65% system-disk ceiling: immutable OS images occupied 67 GiB on `/`, while `/data`
had 245 GiB free. `move_images.py` copies and hashes every image onto `/data`,
keeps existing absolute paths through a persistent bind mount, then removes only
the verified old copies. It requires zero native tasks and no open image handles.

`recover-rejected` is an explicit, incident-bound operator reconciliation of
that proven terminal rejection. It retains the original charged journal and
operation token, verifies the exact original request hash and a fresh empty
operation inventory, checks storage headroom, and records an exclusive durable
attempt marker before one provider request. The acknowledged target is adopted
through the existing journal. It cannot silently retry a timeout or another
ambiguous result. The master URL must use the host-accessible private forward;
the controller's loopback 20889 endpoint exists only in its Docker namespace.
