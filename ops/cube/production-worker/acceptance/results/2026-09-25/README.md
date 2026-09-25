# Actual fresh-worker PostgreSQL and Vite reload acceptance

Both independently serialized live tests passed. All three owned guests
(PostgreSQL source/remix and React reload) were deleted through the controller,
with independent Cube GET404 verification. Final all-state CLI inventory reported
one scanned node and **zero guests**. No model request, customer data, policy
change, power interruption or production acceptance flag was involved.

The exact test binary/source hashes are in `manifest.json`. The temporary source
copy retained full API fixture/migrations layout and current admission guard.
The isolated build was capped at2CPUs/2GiB with networking disabled; the live
guests used reviewed2CPU/2GiB templates. The runner required a fresh consumed
root-only handoff, zero HTTP/CLI inventories, unique evidence directory and flock.

PostgreSQL passed real SQL writes and reads, empty independent remix database,
private data exclusion from published source, distinct transport identity,
pause/resume persistence, supervisor reexec, manifest activation and identical
reload idempotency, same-owner source restore, and private app/home behavior.
The private-home roundtrip imports into the **same guest**, so it does not prove
new-worker disaster restoration or survival of the latest write after power loss.

| PostgreSQL operation | Observed time |
| --- | ---: |
| Create API response | 2,050ms |
| Create through real PostgreSQL app health | 17,391ms |
| Publish source | 71ms |
| Remix API response | 840ms |
| Remix through real PostgreSQL app health | 3,900ms |
| Pause/resume through app health | 1,282ms |

These are individual functional samples, including fresh PostgreSQL startup,
not percentiles or a Docker comparison.

Vite5.4.21 used the exact preinstalled reviewed pnpm patch. Three independent
cold optimizer processes passed source edit, `.env.local` reload with the new
value, and bounded clean shutdown. Reloads took2,283/2,266/2,159ms with a deliberate
2-second synthetic optimizer delay. `pending_requests:1` is sampled before the
successful shutdown; it does not report a leaked request after shutdown.

Private complete logs and fsynced owned-ID journals remain on the worker under
`/data/acceptance-temp/api-20260925-r1/runs/`, directories
`postgres-postgres-01` and `reload-reload-01`. Sanitized reports are retained here.
