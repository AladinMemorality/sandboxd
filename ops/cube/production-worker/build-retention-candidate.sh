#!/usr/bin/env bash
# Run only during an exclusive, empty-worker build handoff. Never installs a binary.
# Bound externally with systemd-run CPUQuota=200% MemoryMax=2G RuntimeMaxSec=900.
set -euo pipefail
umask 077
test "$(hostname)" = baarcha-cube-worker-01
base=/root/cube-production
source_root="$base/CubeSandbox"
candidate="$base/retention-native-candidate"
pin=31d911e430fdf8a8879bd062b8e78066c1a8e89d
installed=/usr/local/services/cubetoolbox/Cubelet/bin/cubelet
test "$(git -C "$source_root" rev-parse HEAD)" = "$pin"
test "$(sha256sum "$installed" | cut -d ' ' -f1)" = 53b09684b59bf1b5609742c1c6b3e17fb22b23ef2320000ddf49716df713346d
test ! -e "$candidate"
inventory=$(cubemastercli list --all --wide)
printf '%s\n' "$inventory" | grep -Eq '^NODES_SCANNED[[:space:]]+1/1$'
printf '%s\n' "$inventory" | grep -Eq '^SANDBOX_COUNT[[:space:]]+0$'
# Both reviewed network source patches must already be present.
for patch in 0001-worker-hard-deny.patch 0002-track-inbound-replies.patch; do
  git -C "$source_root" apply --reverse --check --exclude=CubeNet/src/baarcha_hard_deny.h "$base/security/$patch"
done
mkdir -m 700 "$candidate"
printf '%s\n' "$inventory" > "$candidate/inventory-before.txt"
sha256sum "$installed" > "$candidate/installed-before.sha256"
git -C "$source_root" status --short > "$candidate/source-status.txt"
for component in Cubelet CubeNet pkgs; do cp -a "$source_root/$component" "$candidate/"; done
cd "$candidate"
find Cubelet CubeNet pkgs -type f -print0 | sort -z | xargs -0 sha256sum > copied-source.sha256
# Record all network files, including generated policy and BPF bytes, plus native CoW inputs.
find CubeNet Cubelet/third_party/cubecow -type f -print0 | sort -z | xargs -0 sha256sum > protected-inputs.sha256
(cd "$source_root" && sha256sum -c "$candidate/copied-source.sha256") > copy-verification.txt
for object in mvmtap nodenic localgw; do
  test -s "CubeNet/cubevs/${object}_x86_bpfel.o"
  test -s "CubeNet/cubevs/${object}_x86_bpfel.go"
done
test -s CubeNet/src/baarcha_hard_deny.h
test -s CubeNet/src/baarcha_reply.h
test -s Cubelet/third_party/cubecow/lib/libcubecow.a
sha256sum "$base/0005-retain-unresolved-storage-metadata.patch" > retention-patch.sha256
git apply --check "$base/0005-retain-unresolved-storage-metadata.patch"
git apply "$base/0005-retain-unresolved-storage-metadata.patch"
export PATH="$base/toolchain/go/bin:$PATH"
export GOMAXPROCS=2 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build
test "$(go version | awk '{print $3}')" = go1.25.7
go version > toolchain.txt
cc --version | sed -n '1p' >> toolchain.txt
cd Cubelet
go test -mod=readonly -p 2 ./storage -run 'TestStorageRecoveryRetains|TestRecoverStorageState|TestRecoverSandboxStorage|TestReadBackendFileInfoReturnsMissing' -count=1
go build -mod=readonly -p 2 -trimpath -buildvcs=false -o "$candidate/cubelet-candidate" ./cmd/cubelet
cd "$candidate"
sha256sum -c protected-inputs.sha256 > protected-inputs-after.txt
(cd "$source_root" && sha256sum -c "$candidate/copied-source.sha256") > original-source-after.txt
sha256sum -c installed-before.sha256 > installed-after.txt
find Cubelet CubeNet pkgs -type f -print0 | sort -z | xargs -0 sha256sum > final-source.sha256
sha256sum cubelet-candidate > binary.sha256
go version -m cubelet-candidate > binary-build-info.txt
# Requires native CoW linkage, not the CGO-disabled unsupported stub.
nm cubelet-candidate | awk '$2 == "T" && $3 == "cubecow_init" {found=1} END {exit !found}'
python3 - <<'PY_EMBED'
import mmap,pathlib
root=pathlib.Path.cwd()
objects=[root/'CubeNet/cubevs'/f'{name}_x86_bpfel.o' for name in ('mvmtap','nodenic','localgw')]
with (root/'binary-bpf-verification.txt').open('w') as report:
    for binary in (root/'cubelet-candidate',pathlib.Path('/usr/local/services/cubetoolbox/Cubelet/bin/cubelet')):
        with binary.open('rb') as source, mmap.mmap(source.fileno(),0,access=mmap.ACCESS_READ) as content:
            for obj in objects:
                assert content.find(obj.read_bytes()) >= 0, f'BPF object absent: {obj.name}'
                report.write(f'{binary.name}: {obj.name} exact bytes present\n')
PY_EMBED
ldd cubelet-candidate > binary-libraries.txt
cubemastercli list --all --wide > inventory-after.txt
grep -Eq '^NODES_SCANNED[[:space:]]+1/1$' inventory-after.txt
grep -Eq '^SANDBOX_COUNT[[:space:]]+0$' inventory-after.txt
cat binary.sha256
printf '%s\n' 'Candidate built and tested only; running service and original source unchanged.'
