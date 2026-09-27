# Final customer migration — September 27, 2026

All 72 customer projects now have completed Cube migrations (73 Cube bindings
including the owned fixture). The final two Minecraft projects were migrated
in parallel with original Docker sources and NVMe archives retained. Their
public game tunnels were explicitly deferred by the user.

Production reopened at 10:24:54 UTC after 376.843 seconds. Both dashboards
passed normal authenticated HTML, health, status, source-file and historical-task
checks. Actual Chrome checks then passed for both public pages, with no opening
overlay, JavaScript error or HTTP 5xx. Native migration verified the complete
imported archives before provider commit, including preserved world files.

The restoration report independently reacquired all four operation locks,
verified current canonical provider/journal counts and controller readiness,
and compared the complete restored routing configuration. No worker restart or
new AI task was performed. The Cube-only controller and global new-project
default are separate remaining release work; this evidence does not claim them.
