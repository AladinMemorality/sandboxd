# Preview slot recovery — September 26, 2026

After maintenance ended, Avocall still received wake HTTP 503 because all four
Cube slots held idle projects. The configured 2,100-second idle threshold kept
them occupied for 35 minutes. No keepalive or coding task protected those guests.

At 22:10:45 UTC the normal audited settings API changed only
idle_threshold_seconds from 2100 to 120. Idle reaping remains enabled; keepalive
maximum remains 86400. The four-slot capacity policy is unchanged. Persisted
instance configuration overrides the deployment environment and must be retained
when replacing the controller.

The idle reaper paused the four idle guests. A fresh actual Chrome session then
opened Avocall successfully at 22:11:04 UTC: wake HTTP 200, 5,302 body characters,
no loading overlay or browser errors. Task counts stayed 42 failed / 123 succeeded.
Another real session passed after cohort09 at 22:23:40 UTC.

Private production receipt:
/opt/baarcha-bench/cube-preview-idle-20260926-01/receipt.json.
