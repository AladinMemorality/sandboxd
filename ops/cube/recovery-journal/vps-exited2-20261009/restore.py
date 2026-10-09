import importlib.util,json,os,pathlib,subprocess,sys,time,zipfile
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');stage=root/'exited-recovery-02';sid='01M2QDV0PGEJ1AAKE2MJXGK4P8'
prepared=root/'recovery-prepared-exited2-20261009'/sid
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
def save(name,value):(stage/name).write_text(json.dumps(value))
class LocalWorker(transport.Worker):
 def http(self,method,path,body=None,size=None,headers=None,export=None,timeout=600):
  webhost=self.headers['Host'].replace('3031-',str(self.job.get('web_port',3000))+'-',1)
  if method=='GET' and path=='/' and headers and headers['Host']==webhost:
   return assets.page(self.origin,headers,min(timeout,15))[1]
  return super().http(method,path,body,size,headers,export,timeout)
 def fetch(self,role,receipt):
  path=P(receipt['local_path']);assert path==prepared/(role+'.zip') and not path.is_symlink()
  assert path.stat().st_size==receipt['archive_bytes'] and transport.digest(path)==receipt['sha256'];return path
 def application_checks(self):
  checks={'01M1HJ4EXF1GS6GE3BS9G3ANF3':('gateway','/api/rules'),'01M3C9C0V0MQYNFTMCYS7CCNVC':('postgres','/api/health')}
  if sid not in checks:
   with zipfile.ZipFile(prepared/'workspace.zip') as archive:
    manifest=archive.read('sandbox.yaml').decode() if 'sandbox.yaml' in archive.namelist() else ''
   if 'pnpm exec vite --host 0.0.0.0 --port 3000' not in manifest or '\nworkers:' in manifest:return
   state=self.control('GET','/status');assert not state['active_task']
   restarts={p['name']:p['restarts'] for p in state['processes']}
   headers={**self.headers,'Host':self.headers['Host'].replace('3031-',str(self.job['web_port'])+'-',1)}
   base,html=assets.page(self.origin,headers);static_root=base.rsplit('/',1)[0]+'/'
   queue=assets.entries(html,base,static_root);assert queue,'No local script or stylesheet entries'
   seen=set();total=0;began=time.monotonic()
   while queue:
    path=queue.pop(0)
    if path in seen:continue
    seen.add(path);assert len(seen)<=512
    data=self.http('GET',path,headers=headers,timeout=45);total+=len(data);assert total<=64*1024**2
    queue.extend(p for p in assets.imports(path,data,static_root) if p not in seen)
   after=self.control('GET','/status')
   assert not after['active_task'] and all(p['running'] and p['restarts']==restarts[p['name']] for p in after['processes'])
   memory=assets.guest_memory(self.job['runtime_id']);assert memory['oom_kill']==0,'Guest OOM during compilation'
   save('module-health-'+str(time.time_ns())+'.json',{'passed':True,'memory':memory,'modules':len(seen),'bytes':total,'seconds':time.monotonic()-began,'model_calls':False});return
  name,path=checks[sid];deadline=time.monotonic()+90
  while True:
   try:
    state=self.control('GET','/status');process=next(p for p in state['processes'] if p['name']==name)
    assert process['running']
    headers={**self.headers,'Host':self.headers['Host'].replace('3031-',str(self.job['web_port'])+'-',1)}
    value=json.loads(self.http('GET',path,headers=headers,timeout=5))
    assert (name=='gateway' and isinstance(value,list)) or (name=='postgres' and value.get('ok') is True and value.get('database')=='ready')
    time.sleep(2);after=next(p for p in self.control('GET','/status')['processes'] if p['name']==name)
    assert after['running'] and after['restarts']==process['restarts']
    save('application-health.json',{'worker':name,'path':path,'passed':True,'at':time.time()});return
   except (OSError,RuntimeError,AssertionError,ValueError,KeyError,StopIteration):
    assert time.monotonic()<deadline,'Application worker health did not pass';time.sleep(1)
 def verify(self):
  proof=super().verify();self.application_checks();return proof

request=json.loads((stage/'worker-job.PRIVATE.json').read_text());runtime=request['runtime_id']
assert request['sandbox_id']==sid and request['worker']=='vps'
worker=LocalWorker(request)
if sys.argv[1]=='run':
 ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
 update=subprocess.run(ssh+['python3','/opt/baarcha-vps-export-recovery-2c7e700/worker.py','--container',runtime],capture_output=True,timeout=460)
 (stage/'supervisor-update.PRIVATE.log').write_bytes(update.stdout+update.stderr);assert update.returncode==0
 receipts=[json.loads(line) for line in update.stdout.splitlines()];assert len(receipts)==1 and receipts[0]['status'] in ('updated','current');save('supervisor-update.json',receipts[0])
 proof=worker.restore();save('verified.json',proof)
else:
 assert sys.argv[1]=='continue' and (stage/'verified.json').exists(),'An incomplete import must be independently reconciled'
 worker.application_checks()
print(json.dumps({'verified':True,'sandbox_id':sid,'runtime_id':runtime}),flush=True)
