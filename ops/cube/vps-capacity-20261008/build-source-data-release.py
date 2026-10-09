"""Build the shared source-directory fix after the measured density window."""
import hashlib,json,os,pathlib,shutil,subprocess,tarfile,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');revision='3b1a6f0';out=root/('source-data-release-'+revision)
out.mkdir(mode=0o700)
deadline=time.monotonic()+7200
while not (root/'real-preview-density-50-balanced-07/cleanup.json').exists():
 state=subprocess.check_output(['systemctl','show','baarcha-vps-final-acceptance-43','-p','ActiveState','--value'],text=True).strip()
 assert state!='failed' and time.monotonic()<deadline,'Review acceptance before building'
 time.sleep(5)
assert json.loads((root/'real-preview-density-50-balanced-07/result.json').read_text())['passed']
source=out/'source';source.mkdir()
with tarfile.open(root/('source-'+revision+'.tar.gz')) as tar:tar.extractall(source,filter='data')
base=['docker','run','--rm','--network=none','--cpus=1','--memory=2g','--tmpfs','/tmp:rw,exec,size=512m','-e','GOMAXPROCS=1','-v',str(source)+':/src','-v',str(out)+':/out','-v','baarcha-go122-cache:/root/.cache/go-build','-v','baarcha-go122-modules:/go/pkg/mod','-w','/src/control-plane']
with (out/'tests.log').open('wb') as log:
 subprocess.run(base+['golang:1.22-bookworm','go','test','-p','1','./internal/runtime','-count=1'],stdout=log,stderr=subprocess.STDOUT,check=True)
for command in ['cube-controller','runtimed']:
 subprocess.run(base+['-e','CGO_ENABLED='+('0' if command=='runtimed' else '1'),'golang:1.22-bookworm','go','build','-p','1','-trimpath','-ldflags=-s -w -X main.buildVersion=dev -X main.buildCommit='+revision,'-o','/out/'+command,'./cmd/'+command],check=True)
shutil.copytree(source/'control-plane/migrations',out/'migrations')
(out/'Dockerfile').write_text('FROM sha256:028b53215b95194140bfbb0356e1d6e1cee47f8b42a2fe5e21f7d1966e70aa\nCOPY cube-controller /usr/local/bin/cube-controller\nCOPY migrations/ /usr/local/share/cube-controller/migrations/\nLABEL org.opencontainers.image.revision="'+revision+'"\n')
subprocess.run(['docker','build','--network=none','--pull=false','-t','baarcha-cube-controller:source-data-'+revision,'--iidfile',str(out/'image.id'),str(out)],check=True,env={**os.environ,'DOCKER_BUILDKIT':'0'})
binary=(out/'runtimed').read_bytes()
result={'revision':revision,'image':(out/'image.id').read_text().strip(),'runtimed_sha256':hashlib.sha256(binary).hexdigest(),'runtimed_bytes':len(binary),'tests_passed':True,'b200_contacted':False,'at':time.time()}
(out/'built.json').write_text(json.dumps(result));print(json.dumps(result),flush=True)
