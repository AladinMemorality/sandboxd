#!/usr/bin/env bash
# Build/test ONLY in the bounded private systemd unit described in README.md.
# Does not install or restart anything. Source/protected inputs remain unchanged.
set -euo pipefail
umask 077
base=/root/cube-production
candidate="$base/durable-native-candidate"
source_root="$base/retention-native-candidate"
installed=/usr/local/services/cubetoolbox/Cubelet/bin/cubelet
pin=31d911e430fdf8a8879bd062b8e78066c1a8e89d
test "$(hostname)" = baarcha-cube-worker-01
test "$(git -C "$base/CubeSandbox" rev-parse HEAD)" = "$pin"
test "$(sha256sum "$installed" | cut -d ' ' -f1)" = 254b5e7b11c7c6f865e3e1816c3737a4041424b7e84b5aba7dc406e596eecbed
test -d "$candidate" && test ! -e "$candidate/Cubelet"
for component in Cubelet CubeNet pkgs; do cp -a "$source_root/$component" "$candidate/"; done
cd "$candidate"
find Cubelet CubeNet pkgs -type f -print0 | sort -z | xargs -0 sha256sum > copied-source.sha256
(cd "$source_root" && sha256sum -c "$candidate/copied-source.sha256") > copy-verification.txt
find CubeNet Cubelet/third_party/cubecow -type f -print0 | sort -z | xargs -0 sha256sum > protected-inputs.sha256
sha256sum "$installed" > installed-before.sha256
for patch in 0006-durable-critical-metadata.patch 0007-pause-durable-commit.patch 0008-synchronize-status-readers.patch; do
 sha256sum "$base/durable-build-inputs/$patch" >> patches.sha256
 git apply --check "$base/durable-build-inputs/$patch"
 git apply "$base/durable-build-inputs/$patch"
done
export PATH="$base/toolchain/go/bin:$PATH"
export GOMAXPROCS=2 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build
export CI=true CUBE_SANDBOX_NODE_IP=198.18.0.1
export GOPROXY=off GOTOOLCHAIN=local TMPDIR="$candidate/tmp"
mkdir -m 700 "$TMPDIR"
test "$(go version | awk '{print $3}')" = go1.25.7
go version > toolchain.txt
cc --version | sed -n '1p' >> toolchain.txt
cd Cubelet
# Full affected non-privileged CI suites. Storage's full suite was attempted and
# its baseline S3/background races and loop-mount prerequisites are recorded.
go test -mod=readonly -p 2 -race -timeout 180s ./internal/durability ./pkg/utils ./pkg/store/cubebox ./plugins/cube/internals/cubes ./services/cubebox ./services/server/config ./cmd/cubelet > "$candidate/go-tests.txt" 2>&1
# Exercise all changed catalog/pause paths and retention/recovery regressions.
go test -mod=readonly -p 2 -race -timeout 180s ./storage -run 'Test.*(Catalog|Pause|StorageRecovery|RecoverStorageState|RecoverSandboxStorage|ReadBackendFileInfo)' > "$candidate/storage-focused-tests.txt" 2>&1
go build -mod=readonly -p 2 -trimpath -buildvcs=false -o "$candidate/cubelet-candidate" ./cmd/cubelet
cd "$candidate"
sha256sum -c protected-inputs.sha256 > protected-inputs-after.txt
(cd "$source_root" && sha256sum -c "$candidate/copied-source.sha256") > original-source-after.txt
sha256sum -c installed-before.sha256 > installed-after.txt
find Cubelet CubeNet pkgs -type f -print0 | sort -z | xargs -0 sha256sum > final-source.sha256
sha256sum cubelet-candidate > binary.sha256
go version -m cubelet-candidate > binary-build-info.txt
nm cubelet-candidate | awk '$2 == "T" && $3 == "cubecow_init" {found=1} END {exit !found}'
python3 - <<'PY'
import mmap,pathlib
root=pathlib.Path.cwd()
with (root/'binary-bpf-verification.txt').open('w') as report:
 for binary in (root/'cubelet-candidate',pathlib.Path('/usr/local/services/cubetoolbox/Cubelet/bin/cubelet')):
  with binary.open('rb') as source,mmap.mmap(source.fileno(),0,access=mmap.ACCESS_READ) as data:
   for name in ('mvmtap','nodenic','localgw'):
    obj=root/'CubeNet/cubevs'/f'{name}_x86_bpfel.o'
    assert obj.is_file() and data.find(obj.read_bytes()) >= 0
    report.write(f'{binary.name}: {obj.name} exact bytes present\n')
PY
ldd cubelet-candidate > binary-libraries.txt
cat binary.sha256
printf '%s\n' 'Go candidate built/tests passed; running service unchanged; real pause/power durability gates remain; optional Rust patch is unbuilt.'
