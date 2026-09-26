# Migration upload limit corrected in production

The existing Cube proxy returned413 before forwarding a294,817,991-byte
workspace import. Its general limit was256MiB; the runtimed workspace and home
stream importers both support4GiB. The scoped renderer clones the installed
HTTP/HTTPS host-routing locations for only `/import/private-workspace-v2` and
`/import/private-home`, only PUT and runtimed port3031. Existing Lua routing,
ingress credential checks, upstream filters and logging are retained. The new
locations allow4GiB and disable request spooling and upstream retries.
General256MiB and admin1MiB limits are unchanged.

The candidate passed nginx validation in the actual running image. The change
was applied under all four outer operation locks, the shared worker lifetime
lock and the nested operation lock. The bind-mounted configuration inode was
preserved and nginx reloaded gracefully. No controller or worker restart,
customer source edits or migration occurred during this operation.

Live header-only probes showed both large imports changed413→503, matching the
small invalid-credential request to the paused owned fixture. This proves the
size cap no longer intercepts the request; it does NOT prove a successful upload
or authentication response from an awake guest. The next real migration must
prove large-file transfer. Ordinary uploads and >4GiB imports still returned413;
wrong port404 and wrong method405. Receipt contains exact config/image-container
identity and completion time. The private original and candidate configs are
retained at `/root/baarcha-import-limit-20260926` on the nested worker.

A separate coordinator improvement settles terminal batch CLI failures before
restoring traffic: already-complete projects stay Cube; only known uncommitted
targets may be aborted using the native journaled abort operation, original
Docker bindings/sources stay intact, unattempted projects remain in the source
fence, and accepted projects receive normal API verification. Incomplete or
uncertain journals, failed aborts and changed sources continue to block reopening.
This path has not yet been exercised in a production failure.

All32 cutover tests passed on the native Linux host, including the actual Caddy
routing fixtures and new SQLite-backed partial-failure cases. Local broad tests
could not run three routing cases because Caddy is absent; native runs passed.
