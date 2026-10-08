"""Build the tested queue revision without changing running services."""
import pathlib,subprocess,shutil,os,tarfile
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'queue-release-d463b2d';out.mkdir(mode=0o700)
source=out/'source';source.mkdir()
with tarfile.open(root/'baarcha-queue-release-d463b2d.tar.gz') as t:t.extractall(source,filter='data')
base=['docker','run','--rm','--network=none','--cpus=1','--memory=2g','--tmpfs','/tmp:rw,exec,size=512m','-e','GOMAXPROCS=1','-e','CGO_ENABLED=1','-v',str(source)+':/src','-v',str(out)+':/out','-v','baarcha-go122-cache:/root/.cache/go-build','-v','baarcha-go122-modules:/go/pkg/mod','-w','/src/control-plane','golang:1.22-bookworm']
for command in ['cube-controller','cube-worker-start','cube-worker-stop','cube-relocate']:
 subprocess.run(base+['go','build','-p','1','-trimpath','-ldflags=-s -w -X main.buildVersion=dev -X main.buildCommit=d463b2d','-o','/out/'+command,'./cmd/'+command],check=True)
shutil.copytree(source/'control-plane/migrations',out/'migrations')
(out/'Dockerfile').write_text('FROM sha256:d042906709f5b6088a9535caee0f51d6980dd9f47c4cde97e00e92320175c69c\nCOPY cube-controller /usr/local/bin/cube-controller\nCOPY migrations/ /usr/local/share/cube-controller/migrations/\nLABEL org.opencontainers.image.revision="d463b2d"\n')
subprocess.run(['docker','build','--network=none','--pull=false','-t','baarcha-cube-controller:queue-d463b2d','--iidfile',str(out/'image.id'),str(out)],check=True,env={**os.environ,'DOCKER_BUILDKIT':'0'})
