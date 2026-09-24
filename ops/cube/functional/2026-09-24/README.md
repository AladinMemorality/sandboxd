# Real Cube application Claude/bridge functional check

On 2026-09-24, the opt-in disposable API harness completed an actual Claude Code task in reviewed app template `tpl-ce9efc43b71248d9a0adfb90` (1 vCPU, 1 GiB RAM). This tests the application runtime; it does not use the screenshot worker.

Verified with the actual Cube transport, runtimed supervisor and control-plane task API:

- Agent completed a synthetic command that called the scoped library and file bridge actions, checked the returned private fixture bytes, and wrote `cube-smoke.txt` with the expected content.
- Completed-task model scope was revoked, and replay of its model relay capability returned HTTP 403.
- A second task was cancelled through the ordinary task API; it settled as cancelled and its scope was revoked. Cancellation was requested after two seconds; this does not assert that its sleep tool had already begun executing.
- Production model gateway metering attributed two successful `claude-sonnet-4-6` requests to the temporary verification owner (30,702/32,982 input tokens; 219/0 output tokens). No production tenant project or data was used. Both retained usage rows have outcome `ok`; this run did not retain a separately cancelled model-usage row or a final credit-ledger amount, so it does not prove cancellation billing semantics.
- Guest deletion, temporary verification owner/credential removal, and isolated fixture database schema removal were verified. The dedicated SSH reverse-forward and guest fixture credential file were removed.

The final functional test completed in 30.45 seconds. The 297 ms create-handler figure in the JSON is only that API call, **not** full application/model readiness or a performance comparison.

## Topology and limits

The test uses a fresh in-memory control-plane database and its normal API handlers, with the reviewed Cube template and authenticated reverse channel. Generic egress policy denies every IPv4 destination. Only the existing fixed model/bridge service handlers are wired to a host-side test relay over an operator SSH loopback forward. The relay injects the production model gateway credential on the host; the guest receives only task/project capabilities. Library/file data is a synthetic row in a separate disposable PostgreSQL schema, handled by the platform's real `bridgeFileResponse` with isolated storage dependencies.

Each run was bounded to five minutes, each task to 120 seconds, the relay to four model requests/eight count requests and 2,048 output tokens per model request, and the temporary owner to 1,000 millimes credit. No production platform configuration, tenant files, host network policy or security attestation was changed. Generic registry/public API access, all presets, full publish/remix/journal transitions and production isolation acceptance are separate gates. This harness explicitly records production startup and network isolation acceptance as **false**.

## Test-harness corrections and evidence limits

Two initial runs made no model calls: the disposable relay compared the whole URL and accidentally rejected Claude's supported `?beta=true` query. The fixture now validates that exact query and routes by pathname; the production control-plane relay already supported it.

The successful run then exposed an unhandled `AbortError` in the disposable Node relay's streaming response during task cancellation. The guest/API test still passed. The operator independently queried metering before removing the exact temporary owner and matching isolated schema; `relay-report.json` records this cleanup. The retained relay source adds the stream error listener for reproduction; that final harness-only listener was not exercised in a further paid run. Its ordinary local HTTP cancellation regression passed with `node --test ops/cube/functional/2026-09-24/relay-stream.test.mjs`, including a subsequent successful health request. Per-process library/file counters were lost when the relay exited; the agent's verified file content provides end-to-end evidence for those calls. Do not infer response-stream cleanup correctness solely from this check; the runtime has separate cancellation tests.

`guest-report.json`, `relay-report.json` and `test-output.txt` are sanitized final evidence. `cube_paid_acceptance_test.go` is an operator-only fixture: copy it into a **disposable copy** of `control-plane/internal/api`, compile with that package's test helpers, and invoke only with `CUBE_OPERATOR_FUNCTIONAL=1`. `relay.mjs` requires a private `settings.json` containing the isolated test DB DSN and a staged `file.ts` from the platform bridge implementation. It must run only in the dedicated operator stage, not as a production service. Machine paths in these sources document this run; credentials/config files are deliberately not included.
