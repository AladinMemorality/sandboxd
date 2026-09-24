# Reviewed application migration journal, 2026-09-24

`TestOperatorReviewedTemplateJournalRoundtrip` passed against the actual nested Cube service and reviewed React Pro v3 template `tpl-ce9efc43b71248d9a0adfb90`. The native Linux test completed in **50.70 seconds**; migration including source stop and interruption recovery took 37,779 ms, and rollback after a target write took 756 ms. Docker source shutdown includes its 30-second stop grace. These are functional observations, not a matched performance comparison.

The source was a synthetic Node HTTP application in a disposable Docker container, bounded to one CPU and 1 GiB RAM with no container networking. The target template provides one vCPU and 1 GiB RAM. A private disk SQLite database, owner home, encryption key, and artifact directory were created solely for this test. The configured authenticated reverse broker denied every generic IPv4 destination and had no model or bridge handler. No model requests, production configuration changes, or tenant data were involved.

Verified with the real migration engine and `OfflineBackend`:

- Source home and canonical checkpoint/task history migrated into the actual guest.
- Execution deliberately stopped after the durable `verified` phase; a second engine invocation resumed the journal and completed provider cutover.
- The migrated checkpoint could be reverted, then a new target file was written through the authenticated supervisor.
- Rollback exported current Cube data back into the retained Docker owner home; the new file and canonical task history survived.
- App ID, sandbox ID, preview port and restored Docker provider identity remained stable. The restored Docker HTTP server returned 200.
- Cleanup deleted the target guest and source container. An independent ordinary API/container check found zero remaining guests tagged with the exact fixture sandbox ID, zero matching Docker containers, and no fixture root directories.

The executable source is `control-plane/internal/migration/reviewed_live_test.go`; exact test output is `journal-roundtrip-output.txt`. It requires explicit `CUBE_REVIEWED_MIGRATION_LIVE=1`, the marked disposable VM, and reviewed image/template settings. The test intentionally configures its deny-all functional broker directly; **it does not satisfy production startup attestations, generic external-client compatibility, or network isolation acceptance**. No such gate was enabled. This journal fixture uses the legacy app/history transport, not the separately tested reviewed-home-v2 manifest path. It does not change runtime configuration or execute a new real model task after migration; those are separate acceptance cases.

## Source validation

The final bounded Linux race runs passed on the candidate source:

| Package | Elapsed |
| --- | ---: |
| `internal/egress` | 10.564 s |
| `internal/migration` | 3.986 s |
| `cmd/cube-migrate` | 1.077 s |
| `cmd/sandboxd` | 1.081 s |
| `internal/api` | 25.847 s |

These used cached Go 1.22 in isolated `--network none` containers limited to two CPUs and 2 GiB, with a 768 MiB executable temporary filesystem. The API package additionally needs repository `docs/openapi.yaml` and `traefik/traefik.yml` mounted at their expected relative paths. An earlier API run missing those fixtures failed only the corresponding three repository-contract tests; the complete mounted rerun passed. An earlier adoption mock omitted the required connect-response template identity; commit `bfc36e0` corrected the fixture, and its focused Linux race check passed before the complete suite.

Hosted CI run `36032027907` on `bfc36e0` also passed Go tests, both architecture E2E jobs, console checks, and Linux/macOS installation checks. The evidence commit adds the explicit Docker resource limits used in the actual journal run. A later diagnostic-only fixture correction makes unexpected cleanup database errors fail the test and removes an unused probe construction; that correction does not change the exercised successful migration path.
