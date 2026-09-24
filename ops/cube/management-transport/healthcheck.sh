#!/bin/sh
set -eu
case "${1:-}" in
  api) port=20300 ;;
  proxy) port=20080 ;;
  *) exit 64 ;;
esac
# A safe unauthenticated request must traverse both relays and get an HTTP
# response. This proves transport only, not authenticated Cube health/readiness.
# Keep the request's write side open until the response: QEMU user networking
# drops replies after an early SHUT_WR, including on the direct host forward.
# ignoreeof prevents printf EOF from causing that half-close; -T bounds the probe.
response=$(printf 'GET /healthz HTTP/1.1\r\nHost: cube-transport.invalid\r\nConnection: close\r\n\r\n' |
  /usr/bin/socat -t 3 -T 3 STDIO,ignoreeof "TCP4:127.0.0.1:$port,connect-timeout=2" |
  head -c 1024) || exit 1
printf '%s\n' "$response" | head -n 1 | grep -Eq '^HTTP/1\.[01] (200|204|400|401|403|404) '
