#!/usr/bin/env bash
# Fresh VM only. Build a candidate; never install it, create guests or run packets.
set -euo pipefail
test "$(hostname)" = baarcha-cube-worker-01
build_root=/root/cube-production
source_root="${build_root}/CubeSandbox"
test -d "${build_root}/security"
export PATH="${build_root}/toolchain/go/bin:${build_root}/toolchain/cargo/bin:$PATH"
export CARGO_HOME="${build_root}/toolchain/cargo"
export RUSTUP_HOME="${build_root}/toolchain/rustup"
export RUSTUP_TOOLCHAIN=1.89.0
export GOMAXPROCS=2
test "$(go version | awk '{print $3}')" = go1.25.7
test "$(rustc --version | awk '{print $2}')" = 1.89.0
test ! -e "${source_root}"
git clone --no-checkout --filter=blob:none https://github.com/TencentCloud/CubeSandbox.git "${source_root}"
git -C "${source_root}" checkout --detach 31d911e430fdf8a8879bd062b8e78066c1a8e89d
test "$(git -C "${source_root}" rev-parse HEAD)" = 31d911e430fdf8a8879bd062b8e78066c1a8e89d
cd "${source_root}"
git apply --check "${build_root}/security/0001-worker-hard-deny.patch"
git apply "${build_root}/security/0001-worker-hard-deny.patch"
git apply --check "${build_root}/security/0002-track-inbound-replies.patch"
git apply "${build_root}/security/0002-track-inbound-replies.patch"
python3 - <<'PY'
import json, pathlib, subprocess
root=pathlib.Path('/root/cube-production')
policy=json.loads((root/'protected-addresses.json').read_text())
assert not policy['private_dns_exceptions']
cmd=['python3',str(root/'security/render_hard_deny.py')]
for cidr in policy['management_ipv4_cidrs']:cmd += ['--management-cidr',cidr]
cmd += ['--output',str(root/'CubeSandbox/CubeNet/src/baarcha_hard_deny.h')]
subprocess.run(cmd,check=True)
PY
cd CubeNet/cubevs
GOARCH=amd64 go generate ./...
cd ../../cubecow
cargo +1.89.0 build --locked --release --lib
mkdir -p ../Cubelet/third_party/cubecow/include ../Cubelet/third_party/cubecow/lib
cp include/cubecow.h ../Cubelet/third_party/cubecow/include/
cp target/release/libcubecow.a ../Cubelet/third_party/cubecow/lib/
cd ../Cubelet
go build -trimpath -o "${build_root}/cubelet-candidate" ./cmd/cubelet
sha256sum "${build_root}/cubelet-candidate"
echo 'Candidate built only: not installed, not accepted, no packet tests executed.'
