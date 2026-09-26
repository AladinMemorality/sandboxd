# Parallel cohort recovered; production online

Seven customer projects completed migration and normal authenticated HTML,
JavaScript, source-file and history checks on 2026-09-26 at 19:29 UTC. Six
historical tasks remained accessible. Sources and private archives were retained.
Canonical inventory after restoration was 17 Cube projects (16 customer projects
and one acceptance fixture), 56 Docker projects, 16 complete migration journals,
one aborted journal and zero incomplete journals.

The twelve-project cohort stopped during its second wave when a 294,817,991-byte
workspace archive hit the live Cube proxy's 256 MiB upload cap. The aborted
project remains on Docker; only its uncommitted Cube target was removed through
the native abort operation. Three imported siblings resumed their original
journals and targets successfully. Four selected projects were never attempted.
The upload limit remains unfixed in this receipt.

Maintenance blocked preview wakes and caused the reported Avocall loading
screen. Traffic reopened at 19:29:59 UTC after 583.791 seconds. Actual Chrome
verification of the reported public Avocall URL passed at 19:31:10 UTC: wake200,
iframe HTML/JS200, 5,302 body characters, zero loading overlays and no browser
errors. The project was left awake for the user. This is real live traffic,
not the mocked preview-wake wrapper test. A separate signed-out CompareTel
request returned404; it is not recorded as a successful browser acceptance.

Final restoration unit: `cube-cohort-reopen-20260926-04-r2.service`, exited0.
Invocation: `8afc444867fc4e249e43a807e7709a1c`.
Private final journal:
`/opt/baarcha-cube/worker-01/maintenance/cohort-reopen-20260926-04-r2`.
Private original stage: `/opt/baarcha-bench/cube-cohort-parallel-20260926-01`.
The original operator and first recovery remain historical failed/pending
journals; do not replay them. First recovery stopped before resume because its
input filename collided with an output filename. The second recovery used a
separate input and preserved the same exclusive lock ownership throughout.
Idle failed parents were released only after successful restoration. All four
outer locks were then independently reacquired, full routes and readiness checked.

No paid AI tasks, worker power operations or source retirement occurred.
Global migration, new-project default and controller replacement remain incomplete.
