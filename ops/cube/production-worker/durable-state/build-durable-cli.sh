#!/usr/bin/env bash
set -euo pipefail
umask 077
base=/root/cube-production
source_root=$base/containerd-durable-native-candidate
candidate=$base/durable-cli-candidate
inputs=$base/durable-cli-inputs-r1
test "$(hostname)" = baarcha-cube-worker-01
test -d "$candidate" && test ! -e "$candidate/Cubelet"
for component in Cubelet CubeNet pkgs; do cp -a "$source_root/$component" "$candidate/"; done
cd "$candidate"
find Cubelet CubeNet pkgs -type f -print0 | sort -z | xargs -0 sha256sum > copied-source.sha256
sha256sum "$inputs/0011-refuse-legacy-durable-restore.patch" > patch.sha256
git apply --check "$inputs/0011-refuse-legacy-durable-restore.patch"
git apply "$inputs/0011-refuse-legacy-durable-restore.patch"
export PATH="$base/toolchain/go/bin:$PATH"
export GOMAXPROCS=2 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build
export CI=true CUBE_SANDBOX_NODE_IP=198.18.0.1
export GOPROXY=off GOTOOLCHAIN=local TMPDIR="$candidate/tmp"
mkdir -m 700 "$TMPDIR"
cd Cubelet
go test -mod=readonly -p 2 -race -timeout 90s ./cmd/cubecli/commands/unsafe > "$candidate/cli-tests.txt" 2>&1
go build -mod=readonly -p 2 -trimpath -buildvcs=false -o "$candidate/cubecli-candidate" ./cmd/cubecli
cd "$candidate"
(cd "$source_root" && sha256sum -c "$candidate/copied-source.sha256") > original-source-after.txt
sha256sum cubecli-candidate > binary.sha256
go version -m cubecli-candidate > binary-build-info.txt
cat binary.sha256
