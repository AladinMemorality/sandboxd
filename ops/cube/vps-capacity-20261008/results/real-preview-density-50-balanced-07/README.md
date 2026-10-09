# Repeated VPS-only acceptance

50 existing previews ran together: 49 with 768 MiB limits and one preserved
2 GiB app. All 600 homepage and 1,702 module requests passed. Combined PSS
was 9.05 GiB, median 178 MiB; internal HTTP p95 was 11.4 ms. No new host or
worker OOM and no measured memory pressure. Cleanup preserved canonical
bindings and prior running states. These checks cover serving HTML/module
graphs, not 50 browser sessions, concurrent builds or coding-agent calls.
