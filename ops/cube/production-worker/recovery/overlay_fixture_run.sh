#!/bin/bash
# Rescue VM only; ordinary synthetic filesystem test, no worker/customer disks.
set -euo pipefail
stage=${1:?private synthetic stage required}
[[ $(hostname) == baarcha-cube-rescue && $(id -u) == 0 ]]
[[ "$stage" =~ ^/var/lib/cube-rescue/overlay-fixture-[a-zA-Z0-9-]+$ ]]
[[ -f "$stage/handoff.json" && ! -e "$stage/result.json" ]]
[[ -e /sys/class/net/enp0s2 ]]
for interface in /sys/class/net/*; do
  [[ ${interface##*/} == lo || ${interface##*/} == enp0s2 ]]
done
# The systemd scope must be started asynchronously: SSH disconnects while the
# single guest NIC is down. A host-side VM operation is neither needed nor used.
restore() { ip link set dev enp0s2 up; }
trap restore EXIT HUP INT TERM
ip link set dev enp0s2 down
set +e
timeout --signal=INT --kill-after=20s 240s python3 "$stage/source/overlay_fixture.py" \
  --stage "$stage" --go-validator "$stage/recovery-archive-check" \
  --checker-sha256 e7cc195252880ace691c364ab7ebf3953e4b35530ca283559a4427f68e89d945
status=$?
set -e
printf '%s\n' "$status" > "$stage/exit-status"
exit "$status"
