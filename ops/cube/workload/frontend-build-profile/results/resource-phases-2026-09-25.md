| Phase | Guest memory median / peak MiB | Guest VM PSS peak MiB | VM cgroup file peak MiB | Guest CPU seconds / average / interval peak | RW net MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| before_workload_includes_supervisor_restart | 123.44 / 123.86 | 313.52 | 24.93 | 0.186s / 0.16% / 0.62% | 0.27 |
| template_copy | 221.75 / 339.98 | 701.00 | 357.96 | 9.980s / 99.78% / 101.20% | 37.90 |
| new_frontend_process_to_ready | 446.25 / 446.25 | 871.17 | 542.85 | unavailable | 13.51 |
| warm_api | 629.53 / 629.53 | 952.36 | 718.53 | unavailable | 0.00 |
| build | 842.47 / 842.47 | 1230.10 | 1001.28 | unavailable | -0.00 |
| post_build_api_tail | 627.94 / 628.81 | 1231.16 | 1002.36 | 0.176s / 3.51% / 3.51% | -0.36 |
| hmr | unavailable | — | — | — | — |
| child_cleanup | unavailable | — | — | — | — |
| after_workload_includes_restore_and_other_activity | 390.14 / 392.64 | 1233.08 | 1042.64 | 0.736s / 0.17% / 1.58% | 40.09 |

CPU percentages refer to one core. Build uses the explicit guest build_started/build_finished markers; short phases can lack resource samples. Nested scopes must not be summed. See JSON for actual sample coverage, RSS/cgroup/cache, I/O, pressure, OOM/swap/throttle and collection costs.
