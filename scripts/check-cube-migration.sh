#!/usr/bin/env bash
# Compile/test the branch with the project's Go version, without a host install.
set -euo pipefail
repo_dir="$(cd "$(dirname "$0")/.." && pwd)"
if [ "$#" -eq 0 ]; then
  set -- ./internal/cube ./internal/runtime ./internal/store ./internal/api ./internal/reaper ./internal/reconcile ./internal/snapshot ./internal/wake ./cmd/runtimed ./cmd/sandboxd
fi
exec docker run --rm \
  --mount "type=bind,src=${repo_dir},dst=/repo" \
  --mount type=volume,src=baarcha-cube-go-mod,dst=/go/pkg/mod \
  --mount type=volume,src=baarcha-cube-go-build,dst=/root/.cache/go-build \
  --workdir /repo/control-plane golang:1.22-bookworm go test "$@"
