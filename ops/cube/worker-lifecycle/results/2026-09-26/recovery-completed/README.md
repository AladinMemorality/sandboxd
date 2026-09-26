# Nonempty worker recovery completed, September 26

Production routing was restored at 13:48:51 UTC. The worker is active, controller
health/readiness returned ok/ready, the native observation is consistent, and all
four operator/deployment locks were released. The fleet has 73 projects: one
owned Cube fixture and 72 Docker projects. No customer project was migrated.

The actual cycle drained requests, stopped the controller normally, verified the
paused canary, stopped retained worker services, and issued one QMP powerdown.
The guest emitted SHUTDOWN and the parent observed QEMU exit 0. No forced worker
termination, supervisor status rewrite, guest deletion or grant reset occurred.

The maintenance window encountered three implementation issues:

- The wait4 parser rejected the supervisor reaping an older failed helper after
  QEMU's successful exit. Its original trace and partial QMP messages were kept.
  The repaired verifier accepts unrelated child reaping only after the exact
  target completion. Recovered evidence uses real first-poll and POWERDOWN event
  timestamps and closes the original partial artifact; no attach/send times were
  fabricated. The supervisor's actual worker-lost record remains retained.
- The first native startup attempt refused. A later read-only diagnostic passed
  the configuration, storage, process, controller, evidence, inventory and worker
  readiness checks. The exact initial cause was not exposed by the native CLI;
  startup readiness timing is a hypothesis, not a proven diagnosis. Reconciliation
  then passed from its retained journal. One continuation wrapper also attempted
  to create the existing transition directory and was corrected without another
  worker start or native mutation.
- active-images.json contained a higher-priority stale admission boot ID.
  Inspection proved that was the only environment difference. A journaled CAS
  advanced that field, preserved all other configuration, and recreated the
  exact controller image and management relays. The transition code now handles
  that overlay as a fourth mutable file, including interrupted-CAS regression
  coverage.

Continuations duplicated the original Linux open file descriptions with
pidfd_getfd. Independent contender checks proved all four exclusive locks remained
held continuously. Only owned idle maintenance callers were signaled after
successful routing restoration. Private journals preserve every refusal and
the exact before/after configuration bytes.

## Actual canary acceptance

The 13:49 UTC check verified exact file bytes, UID 1000, 0640/0750 modes, the
newline filename, relative symlink and two-name hardlink relationship. The
previously acknowledged SQL row, original manifest and source hashes, and all
four failed task results/event streams survived. No new AI task or credit grant
was submitted. First authenticated health took 3.242 seconds; owner-authorized
private capture took 1.542 seconds. These are single observations, not fleet
latency percentiles. The JPEG was visually inspected and showed the notes page,
saved SQL acknowledgement and operator marker. Foreign/anonymous access checks
passed. See [canary.json](canary.json).

The handoff's Kanzari public URL returned 404 because the published record is
private. Visibility was not changed. The referenced chat route returned 200;
that only verifies the route shell, not that account's authenticated chat flow.

All 110 worker-lifecycle tests passed as native Linux root with zero skips. The
boot-transition and maintenance follow-ups were installed without restarting
production again. Their hashes, completion identifiers, events and native test
log hash are in [recovery.json](recovery.json). An earlier test archive omitted
service fixture files and produced two FileNotFoundError failures; the complete
archive passed. Those private logs remain retained.

This is a real worker restart and canary continuation pass. It is not a global
rollout, independent paired restore, successful paid AI coding test, capacity
increase, or unattended whole-host reboot acceptance. The global runtime PR
must remain unmerged until those required release gates pass.
