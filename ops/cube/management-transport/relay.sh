#!/bin/sh
set -eu
# No arbitrary address, socat options, environment expansion or shell evaluation.
case "${1:-}" in
  api) port=20300; socket=/run/cube-management/api/channel.sock ;;
  proxy) port=20080; socket=/run/cube-management/proxy/channel.sock ;;
  *) echo "expected api or proxy" >&2; exit 64 ;;
esac
[ "$#" -eq 1 ] || exit 64
[ -S "$socket" ] || { echo "management socket unavailable" >&2; exit 69; }
# -t overrides socat's unsuitable 0.5 s half-close default. Do not add -v/-x:
# they log HTTP credentials. Each accepted connection gets one fixed UDS dial.
exec /usr/bin/socat -t 30 -T 300 \
  "TCP4-LISTEN:$port,bind=127.0.0.1,reuseaddr,fork,max-children=256,backlog=32" \
  "UNIX-CONNECT:$socket,connect-timeout=5"
