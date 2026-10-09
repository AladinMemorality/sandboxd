# Retained disk recovery after an old incremental checkpoint panicked

Density run 05 restored `01M2QDV0PGEJ1AAKE2MJXGK4P8` from an existing
incremental checkpoint. The API initially acknowledged startup, but its guest
kernel panicked in `list_del` before the supervisor check completed. Native state
became stopped (2), while its controller lease remained charged. No host or
worker OOM occurred. This checkpoint predates the deployed full-pause policy.

The exact failed runtime `5373f2de0dac4906987503f741534ccb` was fenced and
retained. A fresh 4,998,406-byte dependency-excluded source archive and a matching
4 GiB native disk clone were verified. There were no historical tasks. The
reviewed offline Session created one replacement, verified source/home/history,
configuration and application health, and atomically retained the app identity.
The replacement is `8bcae4ad09814feba7d0df3bd99937b1`.

The replacement was created only once. The shared weighted resource validation
fix was already installed, so no adoption or ambiguous creation retry was needed.
The controller and local management proxies were restored after commit. The
unbound, quarantined original is enrolled in the retained inactive inventory;
the audit will reject it if it becomes active again.

`wake-check.py` verifies repeated preview module and full-snapshot stop/wake
cycles before resolving density05's cleanup barrier. Failed journals remain
retained. These exact-incident tools are not an unattended recovery service.
No B200 request, model call or source deletion is part of this operation.
