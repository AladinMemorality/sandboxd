"""Build a VPS-only recovery template from the pinned image and tested supervisor."""
import hashlib,importlib.util,json,os,pathlib,shlex,subprocess
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');release=root/'owner-data-release-87a99c7'
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
    assert (release/'built.json').exists() and not (root/'owner-data-template.json').exists()
    assert json.loads((root/'recovery-moves/vps-restore-01m415vt76m0m88gdy2nwwe942/complete.json').read_text())['restored']
    data=(release/'runtimed').read_bytes();expected=hashlib.sha256(data).hexdigest()
    code="""import pathlib,sys,hashlib,subprocess,json,os
root=pathlib.Path('/root/vps-owner-data-template-87a99c7');root.mkdir(mode=0o700)
data=sys.stdin.buffer.read();assert hashlib.sha256(data).hexdigest()==%r
(root/'runtimed').write_bytes(data);(root/'runtimed').chmod(0o755)
base='127.0.0.1:5000/baarcha/react-vite@sha256:b8ece62881111a7c1b6ce3efaaf13960818a53b54cf03c10454dba8938a68c96'
(root/'Dockerfile').write_text('FROM '+base+'\\nCOPY runtimed /usr/local/bin/runtimed\\nLABEL org.opencontainers.image.revision=87a99c7\\n')
tag='127.0.0.1:5000/baarcha/react-vite-ownerdata:87a99c7'
with (root/'image-build.log').open('wb') as log:
 subprocess.run(['docker','build','--network=none','--pull=false','-t',tag,str(root)],stdout=log,stderr=log,check=True,timeout=300,env={**os.environ,'DOCKER_BUILDKIT':'0'})
 subprocess.run(['docker','push',tag],stdout=log,stderr=log,check=True,timeout=300)
old=json.loads(subprocess.check_output(['docker','image','inspect',base]))[0]
new=json.loads(subprocess.check_output(['docker','image','inspect',tag]))[0]
for k in ['Env','Entrypoint','Cmd','User','WorkingDir','ExposedPorts','Volumes','Healthcheck']:
 assert new['Config'].get(k)==old['Config'].get(k),k
image=next(x for x in new['RepoDigests'] if x.startswith('127.0.0.1:5000/baarcha/react-vite-ownerdata@sha256:'))
args=['cubemastercli','tpl','create-from-image','--image',image,'--alias','baarcha-vps-ownerdata-1024-87a99c7','--node','10.0.2.15','--expose-port','3000','--expose-port','3001','--expose-port','3031','--probe','49983','--probe-path','/health','--cpu','1000','--memory','1024','--writable-layer-size','8Gi','--with-cube-ca=false','--deny-out-cidr','0.0.0.0/0','--deny-out-cidr','::/0','--json','--detach']
result=json.loads(subprocess.check_output(args,timeout=90));(root/'create-job.json').write_text(json.dumps(result));job=result['job']['job_id']
with (root/'template-watch.log').open('wb') as log:
 subprocess.run(['cubemastercli','tpl','watch','--job-id',job,'--json'],stdout=log,stderr=log,check=True,timeout=600)
status=json.loads(subprocess.check_output(['cubemastercli','tpl','status','--job-id',job,'--json'],timeout=30));(root/'template-result.json').write_text(json.dumps(status))
assert status['job']['status']=='READY' and status['job']['ready_node_count']==1
print(json.dumps({'ready':True,'template_id':status['job']['template_id'],'image':image,'runtimed_sha256':hashlib.sha256(data).hexdigest(),'memory_mb':1024,'cpu_count':1,'writable_disk_mb':8192,'worker':'vps'}))
"""%expected
    result=subprocess.run(ssh+['python3 -c '+shlex.quote(code)],input=data,capture_output=True,timeout=1100)
    (root/'owner-data-template.PRIVATE.log').write_bytes(result.stderr)
    assert result.returncode==0,'Template creation requires journal review'
    receipt=json.loads(result.stdout);b.atomic(root/'owner-data-template.json',b.encoded(receipt));print(json.dumps(receipt),flush=True)
