# Recovery candidate validation

The pinned Go build passed workerstop/cube/store/pair-verifier race tests and vet.
Its combined runner exited 1 afterward because the initial Python package omitted
two systemd fixture files. Preserve that original result and transcript; it is not
an overall passing job. The separately packaged complete final Python suite
passed all 91 tests as Linux root, with no skips. It includes real private-file
one-use authorization and actual atomic check-mode journal writes.

The disposable wait witness observed only an owned Python parent and sleep child:
real wait4 polling, exit 0, parent exit 1 retained, and safe tracer detach while
both original processes remained alive. It did not power down a VM or mint an
external-clean receipt. The tested WaitWitness/parse_wait4/complete_trace function
ASTs are identical to the final helper; subsequent additions implement receipt
validation and one-use startup authorization, covered separately by unit tests.

`candidate.json` pins the native binary and final source. Native source archive
and complete Python manifests are retained for reproducibility. No real worker
lifecycle operation or unattended boot enabling was performed by these tests.

The root-reviewed live check/cycle is a separate acceptance step. Heavy backup,
Motion backend quiescence and automatic host reboot integration are excluded.
