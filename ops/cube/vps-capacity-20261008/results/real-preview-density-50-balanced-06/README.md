# Fifty real VPS previews — 9 October 2026

Passed with 49 reviewed 768 MiB apps plus the already-running 2 GiB app.
Admission reserved 45 GiB including 128 MiB per VM overhead. The B200 and model
calls were excluded. All prior running/stopped states and bindings were preserved.

The test warmed 1,702 local Vite modules (166.8 MiB), then ran 600 homepage
checks across the 50 live runtimes, with up to ten requests in parallel over
12 rounds. Internal HTTP p95 was 14.9 ms; the maximum was
1.07 seconds. These are worker-side checks, not browser/TLS
end-to-end latency or a coding-agent/build concurrency benchmark.

Measured runtime process PSS totaled 11.11 GiB:
mean 227.6 MiB, median 207.3 MiB,
maximum 529.8 MiB. This excludes the outer VM and other
host services. Host available memory was 10.40 GiB at completion;
worker available memory was 39.08 GiB. Memory PSI
was zero. Host and worker OOM counters did not increase.

Earlier failed density attempts remain retained. Four old checkpoints were
recovered from verified source with originals retained. The native worker now
uses full memory captures for pauses; its three-cycle live canary passed.
Fresh fleet backup, remaining old-checkpoint validation and public browser
return checks are tracked separately and are not implied by this result.
