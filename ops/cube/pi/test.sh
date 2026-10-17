#!/usr/bin/env bash
# Reproduce the adapter/controller suite and real CLI checks without model keys.
# Usage: ops/cube/pi/test.sh baarcha-cube-nextjs-pi:20261017
set -euo pipefail
repo_dir=$(cd "$(dirname "$0")/../../.." && pwd)
image=${1:?Supply the candidate image tag or digest}
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
chmod 755 "$test_dir"
docker run --rm --tmpfs /tmp:rw,exec,size=2g \
  -v "$repo_dir:/repo:ro" -v "$test_dir:/out" -w /repo/control-plane \
  golang:1.25.7-bookworm sh -ec '
    go test ./cmd/runtimed ./internal/api ./internal/agentauth ./internal/authproxy ./cmd/cube-controller
    go test -race ./cmd/runtimed -run "^TestPi|TestLiveInput|TestAgentEnv"
    CGO_ENABLED=0 go test -c -o /out/runtimed.test ./cmd/runtimed
  '
docker run --rm --network none --cpus 2 --memory 2g --user 1000:1000 \
  -e BAARCHA_TEST_PI=1 -v "$test_dir:/test:ro" \
  --entrypoint /test/runtimed.test "$image" \
  -test.run '^TestPi(Stream|Rejects|Followup|EOF|Relay|CLI)' -test.v -test.timeout 90s
