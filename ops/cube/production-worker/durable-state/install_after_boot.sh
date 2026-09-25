#!/usr/bin/env bash
# Historical initial installer, retained only as an explicit deployment refusal.
# a61 failed actual metadata-FD readiness; required0010 supersedes that artifact.
set -euo pipefail
printf '%s\n' 'Historical a61 initial installer is superseded by required0010. Use the reviewed held-worker correction procedure in containerd-correction.md; this script refuses deployment.' >&2
exit 1
