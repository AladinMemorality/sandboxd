# Source review for initial empty host enrollment

Root reviewed the source transition on 2026-09-25. The fixed Go stop/start
coordinators are implemented and tested, including database exclusion, exact
worker/controller generations, paused inventory, and retained failure behavior.
Runtime revision `b452cbf2dc69c2a39f974175fe0037b23b70428d` passed all six
[CI jobs](https://github.com/AladinMemorality/sandboxd/actions/runs/36146085061).
The separate native coordinator race/build evidence is recorded in
[the host candidate](OUTER-ENROLLMENT-CANDIDATE-2026-09-25.md).

The installed nested helper has actually stopped management and Docker without
forced termination. QMP clean powerdown, exact successful QEMU exit and reboot
preserved the same paused sandbox and latest app/home/PostgreSQL data. An
acknowledged pause also survived abrupt worker loss. The recorded actual
[worker results](../production-worker/acceptance/durable-worker/results/2026-09-25-de3/README.md)
and isolated supervisor tests support enabling the source-level coordinator
implementation gate for initial empty enrollment.

This change allows host preflight to reach its existing strict checks. It does
not set any private review flags, adopt the live QEMU process, install units,
create startup/stop receipts, clear a marker or enable Cube routing. Initial
enrollment still requires genuinely empty canonical/provider/admission/task
inventory, retained disk accounting, the real traffic/direct-writer drain and
the first actual production stop/boot/start coordinator cycle. An existing
unclean lifecycle status cannot be bypassed with the first-boot flag.

Install the reviewed new helper on the outer host only when those checks are
ready. Its SHA differs from the currently pinned nested helper
`e1e34848f59f507d0e2ae42b23e2887284248ea0454bd739d827ea716f17e205`.
Keep that functioning nested installation and its manifest unchanged unless
separately regenerated and revalidated. The host implementation gate does not
change nested stop behavior.

Customer activation remains separately blocked on the running-loss recovery
test, current-quota four-workload test, actual lifecycle enrollment, full paired
encrypted off-host backup and application restore, and final fleet acceptance.
At review, native running-loss continuation required recovery and automatic
filesystem preen rejected an orphaned inode in the independent copy. Explicit
clone-only repair is under review; this source change does not claim that the
data recovery passed or relax its checks.
