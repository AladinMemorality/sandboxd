# Homepage redirect and private diagnostics correction

The first real recovery attempt refused the legitimate `www.baarcha.tn /`
308 redirect before stopping the controller, proxy or worker. The parent restored
routing, timers and operator offline bytes; this report does not claim successful
worker recovery.

The corrected helper verifies anonymous home responses during preflight before
fencing: `baarcha.tn /` must return 200 without Location, and `www.baarcha.tn /`
must return 308 with exactly one Location equal to `https://baarcha.tn/`. Curl
never follows redirects. Existing scoped API/preview/Motion probes still require
503 after fencing. Pending failure events retain up to 2048 characters of the
exception in their private journal; native pause stderr is retained in a fresh
0600 `native-pause.stderr`, without terminating the coordinator or discarding its
lock on failure.

Isolated native root Linux run `cube-boot-python-20260926-11` passed 101 tests
in 2.633 seconds with zero skips. Limits: 2 CPU, 256 MiB, 32 tasks, 120 seconds.
Tests exercise exact/wrong/duplicate redirects, preflight-before-fence ordering,
all ten scoped 503 probes, private stderr retention/no overwrite and the real
pending-event entry point. No worker, controller, routing or lifecycle operations
were performed by the test job. Native start binary and all other installed
helpers remain unchanged.

- Maintenance helper: `b1f4c14a1707f8627619880403b3ee806d841588bc4b7999a29a35140f9a967f`
- Source archive: `bf182c048b3a35d12a3e40cd1408d23fe0783f5e639c3524b0b7e94655d7f4fe`
- Test log: `c430ad8be3a60f9cd103a5a1fe5e73d0a44aa416f5af77c231019e91f3b035f9`

Source and test artifacts are preserved at
`/opt/baarcha-bench/cube-boot-python-20260926-11/`. Installation and actual
recovery are separate parent-controlled operations.
