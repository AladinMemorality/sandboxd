set -eu
cd /root/vps-full-pause-20261009-02/Cubelet
export PATH=/root/cube-production/toolchain/go/bin:$PATH
export CI=true CUBE_SANDBOX_NODE_IP=198.18.0.1 GOPROXY=off GOTOOLCHAIN=local
export GOMAXPROCS=2 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build
gofmt -w services/cubebox/pause_cow.go services/cubebox/pause_cow_test.go
go test -mod=readonly -p 2 ./services/cubebox -run 'TestPauseSnapshot|TestNewPause|TestNormalizeSnapshotType' -count=1
go build -mod=readonly -p 2 -trimpath -buildvcs=false -o /root/vps-full-pause-20261009-02/cubelet-candidate ./cmd/cubelet
sha256sum /root/vps-full-pause-20261009-02/cubelet-candidate > /root/vps-full-pause-20261009-02/binary.sha256
