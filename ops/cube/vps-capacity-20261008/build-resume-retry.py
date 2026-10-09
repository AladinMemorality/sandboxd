"""Build the reviewed native-resume retry and offline reconciliation binaries."""
import json,os,pathlib,re,shutil,subprocess,sys,tarfile
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
revision=sys.argv[1];assert re.fullmatch('[a-f0-9]{7,40}',revision)
out=root/('resume-retry-release-'+revision);out.mkdir(mode=0o700)
source=out/'source';source.mkdir()
with tarfile.open(root/('source-'+revision+'.tar.gz')) as tar:tar.extractall(source,filter='data')
base=['docker','run','--rm','--network=none','--cpus=1','--memory=2g','--tmpfs','/tmp:rw,exec,size=512m','-e','GOMAXPROCS=1','-v',str(source)+':/src','-v',str(out)+':/out','-v','baarcha-go122-cache:/root/.cache/go-build','-v','baarcha-go122-modules:/go/pkg/mod','-w','/src/control-plane']
for command in ['cube-controller','cube-migrate']:
    subprocess.run(base+['-e','CGO_ENABLED='+('0' if command=='runtimed' else '1'),'golang:1.22-bookworm','go','build','-p','1','-trimpath','-ldflags=-s -w -X main.buildVersion=dev -X main.buildCommit='+revision,'-o','/out/'+command,'./cmd/'+command],check=True)
shutil.copytree(source/'control-plane/migrations',out/'migrations')
(out/'Dockerfile').write_text('FROM sha256:0d4db42569301df88087823bd40046f090c033bc564ee99c81e41e7f9650c8ab\nCOPY cube-controller /usr/local/bin/cube-controller\nCOPY migrations/ /usr/local/share/cube-controller/migrations/\nLABEL org.opencontainers.image.revision="'+revision+'"\n')
subprocess.run(['docker','build','--network=none','--pull=false','-t','baarcha-cube-controller:recovery-'+revision,'--iidfile',str(out/'image.id'),str(out)],check=True,env={**os.environ,'DOCKER_BUILDKIT':'0'})
(out/'built.json').write_text(json.dumps({'revision':revision,'image':(out/'image.id').read_text().strip(),'offline_reconciler_built':True}))
