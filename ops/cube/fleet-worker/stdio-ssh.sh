#!/usr/bin/env bash
# Operator recovery transport through the hypervisor's loopback. No guest keys
# are copied to B200. The SSH client must still verify the guest host key.
set -euo pipefail
exec docker exec -i baarcha-cube-worker-b200-01 /worker/ssh-relay
