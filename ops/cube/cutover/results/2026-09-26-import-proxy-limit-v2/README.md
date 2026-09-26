# Framed home uploads supported

The actual running Cube proxy now includes the exact `/import/private-home-v2`
location for PUT requests on runtimed port3031. Its bound is4GiB plus32KiB and
four bytes for the manifest frame. Existing workspace/home-v1 limits remain4GiB;
ordinary uploads remain256MiB. Lua authorization/routing and response handling
are cloned unchanged from the installed routes. Streaming requests are not
spooled or replayed to another upstream.

Actual nginx validation passed. The existing bind-mounted inode was updated
under the four outer locks, shared worker lifetime lock and nested operator
lock, then gracefully reloaded without controller or worker restart. Header-only
probes changed281MiB home-v2 requests413→503, matching the paused fixture's
ordinary routing response. The exact framed bound passes the proxy size check;
one byte beyond it returns413. This is not a completed large-body transfer.
Private original/config/proof: `/root/baarcha-import-limit-v2-20260926` nested.
