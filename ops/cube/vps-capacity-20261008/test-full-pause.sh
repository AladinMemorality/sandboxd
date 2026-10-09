#!/bin/bash
set -eu
cd /root/vps-full-pause-20261009-02/Cubelet
export PATH=/root/cube-production/toolchain/go/bin:$PATH
export CI=true CUBE_SANDBOX_NODE_IP=198.18.0.1 GOPROXY=off GOTOOLCHAIN=local
export GOMAXPROCS=2 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build
go test -mod=readonly -p 2 -race -timeout 180s ./internal/durability ./pkg/utils ./pkg/store/cubebox ./plugins/cube/internals/cubes ./services/cubebox ./services/server/config ./cmd/cubelet ./plugins/metadata ./plugins/backup > ../regression-tests.txt 2>&1
go test -mod=readonly -p 2 -race -timeout 180s ./storage -run 'Test.*(Catalog|Pause|StorageRecovery|RecoverStorageState|RecoverSandboxStorage|ReadBackendFileInfo)' > ../storage-tests.txt 2>&1
python3 - <<'CHECK'
import pathlib,hashlib,mmap,json
r=pathlib.Path('/root/vps-full-pause-20261009-02');old=pathlib.Path('/root/cube-production/containerd-durable-native-candidate')
changed=[]
for line in (old/'final-source.sha256').read_text().splitlines():
 sha,name=line.split(None,1);name=name.lstrip('*')
 path=r/name
 if hashlib.sha256(path.read_bytes()).hexdigest()!=sha:changed.append(name)
assert sorted(changed)==['Cubelet/services/cubebox/pause_cow.go','Cubelet/services/cubebox/pause_cow_test.go'],changed
with (r/'cubelet-candidate').open('rb') as f,mmap.mmap(f.fileno(),0,access=mmap.ACCESS_READ) as data:
 for name in ('mvmtap','nodenic','localgw'):
  obj=r/'CubeNet/cubevs'/f'{name}_x86_bpfel.o';assert data.find(obj.read_bytes())>=0
(r/'regression-passed.json').write_text(json.dumps({'passed':True,'changed_files':changed,'embedded_bpf_preserved':True,'native_source_sha256':hashlib.sha256((old/'final-source.sha256').read_bytes()).hexdigest(),'candidate_sha256':hashlib.sha256((r/'cubelet-candidate').read_bytes()).hexdigest()}))
CHECK
