# Motion guest acceptance

This operator fixture verifies the deployed Motion worker through two newly
created Cube guests using the exact reviewed Motion template. It does not
migrate a customer, change the provider default, or replace the controller.

The native Go runner uses the canonical admission store and its unchanged
four-slot/storage contract. Synthetic application identities and migration
journals live in a separate private SQLite database. The actual migration
broker grants the fixed Motion capability to just one synthetic application;
the sibling receives no worker service. Both guests run a pinned copy of the
existing customer's frontend proxy. Their direct network policy denies all
egress.

`probe.mjs` sends every acceptance request through the owner's private Cube
application ingress. It checks worker status and projects, creates and edits an
owned synthetic film, uploads exactly 50 MiB of valid MP4 data, rejects one byte
over that limit without changing the film, and verifies HEAD, prefix/suffix
ranges, a complete download hash and decoding with ffmpeg. It submits no
plan/render/narrate/clone request. The runner then verifies that changing the
private migration journal's phase revokes the old channel generation, that
reattachment restores access, and that detachment removes access again.

Import and configuration both trigger asynchronous supervisor restarts. The
runner waits for authenticated boot/configuration acknowledgement before the
next operation. The first production fixture exposed a missing wait in the
initial harness: the guest was cleaned up and production restored, without
creating a worker film. This is separate from a customer migration failure.

`window.py` uses the existing current-generation maintenance fence, task-aware
preview pauses and same-controller restoration. It pins inputs, controller,
worker process/boot, all current bindings and full live routing. It never powers
off the Cube worker. Its foreground native process must hold the exclusive
canonical database guard; do not inspect that SQLite database from another
process while it runs. Monitor the unit, `current.json` and private receipts.

On completion or a settled test failure, cleanup checks exact guest metadata,
provider 404 and released admission. `cleanup.mjs` removes only the newly created
UUID carrying the unique owned marker, after checking the existing worker
projects. A completed HTTP response is required before automatic film cleanup;
an interrupted upload can have a worker-side tail even after the guest closes.
An uncertain create is not retried: its persisted intent and marker support
explicit reconciliation. Any unknown guest allocation retains its
admission and prevents automatic restoration. All customer canonical tables
must remain unchanged; only the exact owned admission records and their storage
accounting may differ. The original controller and complete routes are restored
only after these checks. A terminal successful maintenance unit can therefore
still contain `accepted: false`; inspect that result rather than assuming pass.

Build `main.go` and `main_test.go` under
`control-plane/cmd/motion-acceptance` in an isolated reviewed Go build stage.
The runtime executable runs natively on the host, with a private configuration
prepared from the pinned controller environment. Secrets are never passed in
argv or logged. Every run and maintenance directory is unique and not replayed.

Local verification:

```sh
node --test ops/cube/motion-studio/acceptance/probe.test.mjs
python3 -m unittest discover -s ops/cube/motion-studio/acceptance -p 'test_*.py'
# In the isolated build stage, after copying the two Go sources:
go test -race ./cmd/motion-acceptance
go build -o /review/motion-acceptance ./cmd/motion-acceptance
```

These tests validate the harness, its restart sequencing and cleanup refusal
paths. The private production phase receipts establish actual guest/worker
acceptance; local tests alone do not.
