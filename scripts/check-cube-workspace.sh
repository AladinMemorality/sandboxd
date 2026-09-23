#!/usr/bin/env bash
# Real package-manager and guest-UID tests; no registry network needed.
set -euo pipefail
repo_dir="${CUBE_WORKSPACE_TEST_REPO:-$(cd "$(dirname "$0")/.." && pwd)}"
base_image="${CUBE_WORKSPACE_TEST_BASE_IMAGE:-sandboxd-base:0.3.0}"
test_volume="cube-workspace-tests-$$"
cleanup() { docker volume rm "$test_volume" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker volume create "$test_volume" >/dev/null
docker run --rm \
 --mount "type=bind,src=${repo_dir},dst=/repo,readonly" \
 --mount "type=volume,src=${test_volume},dst=/out" \
 --mount type=volume,src=baarcha-cube-go-mod,dst=/go/pkg/mod \
 --mount type=volume,src=baarcha-cube-go-build,dst=/root/.cache/go-build \
 --workdir /repo/control-plane golang:1.22-bookworm \
 go test -c -o /out/runtimed.test ./cmd/runtimed
docker run --rm --network none --user 0 \
 --mount "type=volume,src=${test_volume},dst=/out,readonly" \
 --entrypoint /out/runtimed.test "$base_image" -test.v \
 -test.run 'TestDependency|TestSourceKeepsMatchingFreshPythonEnvironment|TestQuiescenceStopsDetachedSessionWriters'
docker run --rm --network none --user 1000 --env RUNTIMED_CUBE_GUEST=1 \
 --mount "type=volume,src=${test_volume},dst=/out,readonly" \
 --entrypoint /out/runtimed.test "$base_image" -test.v \
 -test.run '^TestPrivateWorkspaceRequiresQuiescenceAndFencesMutation$|^TestWorkspaceFenceDoesNotBlockStatusBehindPreparation$|^TestPrivateTaskHistoryGuestTransportRoundtrip$'
