import pathlib,subprocess,shutil,os
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'weighted-release-02';out.mkdir(mode=0o700)
base=['docker','run','--rm','--network=none','--cpus=2','--memory=2g','--tmpfs','/tmp:rw,exec,size=512m','-e','GOMAXPROCS=2','-e','CGO_ENABLED=1','-v',str(root/'source')+':/src','-v',str(out)+':/out','-v','baarcha-go122-cache:/root/.cache/go-build','-v','baarcha-go122-modules:/go/pkg/mod','-w','/src/control-plane','golang:1.22-bookworm']
for command in ['cube-controller','cube-worker-start','cube-worker-stop','cube-relocate']:
 subprocess.run(base+['go','build','-p','2','-trimpath','-ldflags=-s -w -X main.buildVersion=dev -X main.buildCommit=c9811a8','-o','/out/'+command,'./cmd/'+command],check=True)
shutil.copytree(root/'source/control-plane/migrations',out/'migrations')
text=(root/'controller-release/Dockerfile').read_text().replace('6cd111d','c9811a8')
(out/'Dockerfile').write_text(text)
subprocess.run(['docker','build','--network=none','--pull=false','-t','baarcha-cube-controller:weighted-c9811a8','--iidfile',str(out/'image.id'),str(out)],check=True,env={**os.environ,'DOCKER_BUILDKIT':'0'})
