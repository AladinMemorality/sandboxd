# Remaining preview latency and module discovery

After fixing source-read lock contention, this project's preview still crosses a
worker link with approximately 280–300 ms round-trip latency. Controller
permission checks measured 2–6 ms locally; warm public asset responses measured
roughly 488–516 ms to first byte. Permissions must continue to be checked for
every request. These measurements are from the VPS, not every visitor location.

The gateway's development-module scanner excluded newlines inside named imports.
The actual radix-ui module had four dependencies but the old scanner found none;
react-dom_client had three but the scanner found only one. The browser therefore
had to discover additional dependency chunks after their parents downloaded.

The scanner now follows multiline imports and re-exports. The regression fixture
covers a transitive multiline dependency, preserving its version query, and
continues to reject external and non-module paths. Scope, request/byte/time
limits, permission checks, private no-store responses, and live source are unchanged.

Validation: gateway race tests and go vet. Production evidence and per-worker
binary backups are under /opt/baarcha-bench/preview-preloads-20261001. Deploy only
the gateway binary using ops/cube/preview-preloads/deploy.py with an exact baseline
and candidate digest. No controller restart or runtime lifecycle mutation is needed.

Other remaining costs include geographic network round trips, unbundled development
modules, and orchestration when a sleeping app resumes. This change does not claim
to eliminate those costs. Compare repeated browser runs rather than a single cold
open; a cold browser session is not necessarily a suspended runtime.
