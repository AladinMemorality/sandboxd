# Parallel migration runner prepared

The user requested larger batches and parallel migration after the successful
production cohorts. The version2 coordinator accepts12 projects in waves of up
to4 concurrent migrations. Each wave uses one native CLI and shared Store,
retaining the existing exclusive database-user fence and serialized SQLite
writer. Archive, import, verification and readiness work overlap. Only provider
creation acknowledgments are serialized, preserving the one-pending-create
admission contract. All already-started siblings finish before failure returns.

Native Go race suites passed for `./cmd/cube-migrate` and
`./internal/migration`. A real shared-SQLite/filesystem test requires four
archives to enter before any may finish, verifies independent source bytes,
proves creation does not overlap, and checks that one corrupt target remains
Docker while three siblings commit and pause. Correcting and resuming the
failed target does not create a second runtime. Invalid/duplicate batches are
rejected before mutation. All12 native Python cohort/template tests passed.

Built CLI SHA256:
`2e37ecf4fd212aa86c56a3af11bf6bb940ff12c4e57825cdd7a4b17067f1f1ec`.
Build image:
`sha256:3d699e4d15d0f8f13c9195c0632a16702b8cbdece2955af1c23b37ae5d55a253`.
The first test-launch attempt hit a noexec temporary mount; no tests or service
mutations ran in that attempt. Using an executable bounded temporary filesystem
allowed both race suites to run successfully and the binary to build.

The12-project production plan passed live read-only preflight with all10
existing Cube projects paused and no customer tasks or pending writers.
Preflight unit `cube-cohort-check-20260926-04.service` exited0; invocation
`c50eeb34ffd54aa9862fb045cc68ea31`. Plan SHA256:
`d358dcfdc5a648dff424f2364233790460c22402b48d65a8a6352b433e3680d3`.
Private stage `/opt/baarcha-bench/cube-cohort-parallel-20260926-01`.
This preparation record alone does not claim production parallel acceptance.
