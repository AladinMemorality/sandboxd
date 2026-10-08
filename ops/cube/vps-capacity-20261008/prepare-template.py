import contextlib,fcntl,json,pathlib,subprocess,shlex,os
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008');root.mkdir(mode=0o700,exist_ok=True)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-o','BatchMode=yes','root@127.0.0.1']
with contextlib.ExitStack() as stack:
 for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
  f=stack.enter_context(open(name,'a'));fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
 code="""import json,pathlib,subprocess,os
p=pathlib.Path('/root/vps-50-profiles-20261008');p.mkdir(mode=0o700,exist_ok=True)
jobfile=p/'react-vite-512-job.json'
assert not jobfile.exists(),'prior job needs reconciliation'
s=os.statvfs('/data');assert s.f_bavail*s.f_frsize>160*1024**3
v=json.loads(subprocess.check_output(['cubemastercli','tpl','info','--template-id','tpl-5abec4cb4fcc41cc8e611f69','--include-request','--json']))
assert v['status']=='READY' and v['image_info']=='127.0.0.1:5000/baarcha/react-vite@sha256:b8ece62881111a7c1b6ce3efaaf13960818a53b54cf03c10454dba8938a68c96'
args=['cubemastercli','tpl','create-from-image','--image',v['image_info'],'--alias','baarcha-vps-react-512-20261008','--node','10.0.2.15','--expose-port','3000','--expose-port','3001','--expose-port','3031','--probe','49983','--probe-path','/health','--cpu','1000','--memory','512','--writable-layer-size','4Gi','--with-cube-ca=false','--deny-out-cidr','0.0.0.0/0','--deny-out-cidr','::/0','--json','--detach']
r=json.loads(subprocess.check_output(args,timeout=90));jobfile.write_text(json.dumps(r,indent=2));print(json.dumps(r),flush=True)
job=r['job'];log=(p/'react-vite-512-watch.log').open('w')
try:subprocess.run(['cubemastercli','tpl','watch','--job-id',job['job_id'],'--json'],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=600)
finally:
 status=json.loads(subprocess.check_output(['cubemastercli','tpl','status','--job-id',job['job_id'],'--json'],timeout=20));(p/'react-vite-512-result.json').write_text(json.dumps(status,indent=2));print(json.dumps(status),flush=True)
assert status['job']['status']=='READY'
"""
 r=subprocess.run(ssh+['python3 -c '+shlex.quote(code)],stdout=(root/'template-512.log').open('w'),stderr=subprocess.STDOUT)
 (root/'template-512-exit.json').write_text(json.dumps({'returncode':r.returncode}))
 raise SystemExit(r.returncode)
