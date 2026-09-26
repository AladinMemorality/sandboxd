# Cube controller candidate verification

Candidate only; no production controller replacement or customer migration.

The pinned Go 1.22 Linux container ran `go test -race -json` and `go vet`
on `./internal/cubeconfig ./internal/store ./internal/api ./internal/authproxy
./cmd/cube-controller ./cmd/sandboxd`, then built both entrypoints.
The test log is retained privately at
`/opt/baarcha-bench/cube-controller-review-20260926-03/tests.jsonl`.
Its SHA-256 and the exact formatted source manifest are recorded here.

The actual-process test ran as Linux root without PATH, inherited credentials,
Docker CLI, Docker socket or production state. It verified fresh worker
observation, authenticated API, exclusion of the old daemon maintenance lock
and graceful SIGTERM release. Provider requests used a local read-only fixture.
Two opt-in live Cube tests were skipped; these are not production acceptance.
