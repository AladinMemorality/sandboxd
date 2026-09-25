#!/usr/bin/env bash
set -euo pipefail
umask 077
base=/root/cube-production
source_root=$base/durable-native-candidate
candidate=$base/containerd-durable-native-candidate
inputs=$base/containerd-durable-build-inputs-r5
installed=/usr/local/services/cubetoolbox/Cubelet/bin/cubelet
test "$(hostname)" = baarcha-cube-worker-01
test "$(sha256sum "$installed" | cut -d ' ' -f1)" = a61a43c531b8e7854dd8ee064db0d1e160d42fcb4d50e7c4b3c98d447e63e75a
test -d "$candidate" && test ! -e "$candidate/Cubelet"
for component in Cubelet CubeNet pkgs; do cp -a "$source_root/$component" "$candidate/"; done
cd "$candidate"
find Cubelet CubeNet pkgs -type f -print0 | sort -z | xargs -0 sha256sum > copied-source.sha256
(cd "$source_root" && sha256sum -c "$candidate/copied-source.sha256") > copy-verification.txt
find CubeNet Cubelet/third_party/cubecow -type f -print0 | sort -z | xargs -0 sha256sum > protected-inputs.sha256
sha256sum "$installed" > installed-before.sha256
sha256sum "$inputs/0010-containerd-durable-root.patch" > patch.sha256
git apply --check "$inputs/0010-containerd-durable-root.patch"
git apply "$inputs/0010-containerd-durable-root.patch"
export PATH="$base/toolchain/go/bin:$PATH"
export GOMAXPROCS=2 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build
export CI=true CUBE_SANDBOX_NODE_IP=198.18.0.1
export GOPROXY=off GOTOOLCHAIN=local TMPDIR="$candidate/tmp"
mkdir -m 700 "$TMPDIR"
go version > toolchain.txt
cc --version | sed -n '1p' >> toolchain.txt
cd Cubelet
# Real plugin registration/config decoder/InitFn/database operations, no mount.
go test -mod=readonly -p 2 -race -timeout 180s ./plugins/metadata ./plugins/backup > "$candidate/plugin-tests.txt" 2>&1
# Verify the same new tests expose the old actual plugin bug, not just config shape.
mkdir -p internal/durable_regression_baseline
for name in metadata backup; do
 cp -a "$source_root/Cubelet/plugins/$name" "internal/durable_regression_baseline/$name"
 cp "plugins/$name/"*test.go "internal/durable_regression_baseline/$name/"
done
set +e
go test -mod=readonly -p 2 -race -timeout 90s ./internal/durable_regression_baseline/metadata -run TestRegisteredMetadataPluginOpensAndReopensDurableRoot > "$candidate/baseline-metadata.txt" 2>&1
metadata_exit=$?
go test -mod=readonly -p 2 -race -timeout 90s ./internal/durable_regression_baseline/backup -run TestRegisteredBackupUsesActualPersistentMetadataDatabase > "$candidate/baseline-backup.txt" 2>&1
backup_exit=$?
set -e
test "$metadata_exit" != 0 && grep -q 'actual DB path' "$candidate/baseline-metadata.txt"
test "$backup_exit" != 0 && grep -q 'backup did not use actual open database' "$candidate/baseline-backup.txt"
go test -mod=readonly -p 2 -race -timeout 180s ./internal/durability ./pkg/utils ./pkg/store/cubebox ./plugins/cube/internals/cubes ./services/cubebox ./services/server/config ./cmd/cubelet ./plugins/metadata ./plugins/backup > "$candidate/go-tests.txt" 2>&1
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
