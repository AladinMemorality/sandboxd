# Finite anonymous-map kernel decision fixture

This fixture executed once on the fresh reviewed worker after a separate review of its scope. It did **not** repeat the earlier rejected attached/raw-packet trials. It opens no raw or network socket, attaches no program, pins no map, reuses no deployed map, and transmits no packet. Linux `BPF_PROG_TEST_RUN` evaluates local synthetic buffers against newly loaded, unattached SCHED_CLS programs and fresh anonymous maps.

Result: **20 kernel cases passed**, one expiry case was inconclusive because kernel uptime was below the real one-hour idle TTL, and map-write failure remains **userspace helper proof only**. The kernel run made 34 program-test calls in 311 ms. These results do not independently authorize production or establish complete deployed network isolation.

## Identity and scope

The three production ELF SHA256 values are hardcoded in `main.go` and recorded in `results/elf-inventory.json`. They come from upstream `31d911e430fdf8a8879bd062b8e78066c1a8e89d` plus the reviewed hard-deny/reply-permission patches and operator-generated destination policy. The running candidate executable was `53b09684b59bf1b5609742c1c6b3e17fb22b23ef2320000ddf49716df713346d` before and after the run.

`classifier_fixture.bpf.c` is a separate, test-only wrapper compiled against the exact production `session.h`, `map.h`, `cubevs.h`, generated hard-deny header and vmlinux header. It tests allow-rule precedence and cached-session rejection directly, avoiding a false negative caused merely by missing synthetic NAT routes. Its object SHA256 is `29046a7422fc4ab221dc84deba1cfbfacd4bf548c1de40ab52e75f6e0201a7ab`; it is never part of the worker executable.

Only test constants for node/gateway addresses and ifindices are rewritten. Map key/value ABIs and types remain intact; every map is made anonymous and capped at 64 entries. Collections share only maps allocated by this process. Positive controls require an actual ingress-created permission and a valid matching reply, including destination and non-RST checks. The classifier positive control has a matching deny rule, so it depends on the permissive allow rule.

The 22 cases match `../scoped-kernel-cases.json`. The declared four source ports are distributed across specific negative cases, not expanded into a Cartesian test matrix. Exact input constants, commands, resource bounds and source hashes are in `results/execution-manifest.json`.

## Validation and cleanup

- Linux amd64 Go build passed with cilium/ebpf v0.17.3, matching the candidate.
- Three pure fixture tests passed as unprivileged `nobody`: valid IPv4/IPv6/TCP checksums, tuple ABI layout, and anonymous map sizing/pinning.
- Default executable mode parsed and hash-checked the ELF inventory as `nobody`, without BPF syscalls.
- The reviewed execution used a transient systemd unit with CPUQuota=200%, MemoryMax=2G, RuntimeMaxSec=120, TasksMax=32 and LimitNOFILE=512. Internal bounds are 22 cases, four calls per case, 88 calls total and 110 seconds between calls. No live-frame flag, CPU selection or batch flag is passed to program-test-run.
- Every one of the 37 existing program metadata records and 1,521 existing map metadata records was byte-for-byte identical before and after. No additional program or map ID remained; TC attachment records were unchanged. The transient unit was gone/inactive afterward. See `results/loaded-provenance.json`.

Existing map values were deliberately not dumped. Metadata equality establishes unchanged identity and definitions, not immutable application data. Program tags and source object hashes alone do not prove byte-for-byte equality of relocated loaded instructions. The fixture uses pinned source object hashes plus running executable provenance and separately records deployed program/map identities.

The initial launcher attempt failed before execution because systemd-run requires an absolute executable path; the corrected invocation in the manifest is the only kernel run. No permission-denied retry or expanded test was involved.

## Coverage limits

`reply-expired` is explicitly `kernel_inconclusive_userspace_clock_proof`; there was no clock change, wait, unsigned timestamp wrap or fabricated future timestamp. `reply-map-write-failure` is explicitly `userspace_helper_proof_only`; there was no map freezing, kernel fault injection or resource exhaustion. The existing fake-map/clock helper test in `../test_reply_permissions.py` supplies those separate unit proofs.

The reply cache uses a one-hour idle TTL refreshed by accepted replies. It does not provide complete TCP sequence tracking, FIN/RST removal or an absolute one-hour lifetime. Anonymous decisions do not test real interface attachment, deployed DNS, lifecycle reconnection, application functionality, or ordinary socket connectivity. Those require separately scoped acceptance with owned guests and listeners.

## Reproduction

The executable defaults to metadata parsing only. Do not treat these reproduction instructions as authorization for another kernel run. Execution requires separate scope review, root, `--execute-reviewed-anonymous` and `CUBE_ANONYMOUS_REVIEW=explicitly-approved-finite-fixture`; the exact reviewed command is retained in the manifest. Both result authorization flags remain false, regardless of the process exit code.

The exact object bundle and raw before/after inventory are retained privately at `/root/cube-production/anonymous-review-20260924` on the fresh worker. The sanitized repository evidence contains all case results, hashes and relevant deployed map/program metadata, without credentials or tenant data.
