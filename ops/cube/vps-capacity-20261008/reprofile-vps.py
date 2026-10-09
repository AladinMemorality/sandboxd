"""Reprofile a stopped VPS app using a fresh verified export of its current state."""
import contextlib,fcntl,importlib.util,json,os,pathlib,select,shutil,shlex,sqlite3,subprocess,sys,time,traceback
from migration_lifecycle import lifecycle
from maintenance_account import account_maintenance
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sid=sys.argv[1];assert len(sid)==26 and sid.isalnum()
profile=sys.argv[3] if len(sys.argv)>3 else 'balanced'
assert profile in ('balanced','standard','large')
profile_memory={'balanced':768,'standard':1024,'large':2048}
receipt=json.loads((root/'balanced-template-a583d45.json').read_text())
deployed=json.loads((root/'balanced-release-a583d45/deployed.json').read_text())
assert receipt['ready'] and receipt['worker']=='vps' and receipt['memory_mb']==768
assert deployed['deployed'] and deployed['template_id']==receipt['template_id']
templates={'balanced':receipt['template_id'],'standard':'tpl-86350411460a47db8ffe6ff5','large':'tpl-5abec4cb4fcc41cc8e611f69'}
source=None
spec=importlib.util.spec_from_file_location('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
# Do not add another guest while the host is stalled reclaiming/compacting.
# This runs before any provider operation or journal creation.
pressure_deadline=time.monotonic()+1800
while True:
 full=next(line for line in P('/proc/pressure/memory').read_text().splitlines() if line.startswith('full '))
 avg10=float(dict(part.split('=',1) for part in full.split()[1:])['avg10'])
 available=int(next(line.split()[1] for line in P('/proc/meminfo').read_text().splitlines() if line.startswith('MemAvailable:')))
 if avg10<2 and available>8*1024**2:break
 assert time.monotonic()<pressure_deadline,'Host pressure remains high; restore postponed'
 time.sleep(5)
attempt=sys.argv[2] if len(sys.argv)>2 else '01'
assert attempt.isalnum() and len(attempt)<=8
job=root/'recovery-moves'/('vps-reprofile-'+sid.lower()+'-'+str(profile_memory[profile])+'-'+attempt)
BIN=root/'cube-relocate-reprofile';migrations=root/'queue-release-d463b2d/source/control-plane/migrations'
def save(name,value):b.atomic(job/name,b.encoded(value))
def rows(query,args=()):
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=10)) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(query,args)]
def cli(action,**extra):
 request={'Action':action,'ID':job.name,'Directory':str(job),'Migrations':str(migrations),**extra}
 with lifecycle():
  p=subprocess.run([str(BIN)],input=json.dumps(request).encode(),capture_output=True,timeout=200)
 if p.returncode:save('cli-'+action+'-failed.json',{'exit_code':p.returncode});raise RuntimeError('relocation '+action+' refused')
 return json.loads(p.stdout)
class SourceWorker(transport.Worker):
 def inventory(self):
  manifest=super().inventory()
  guest="import pathlib,json; print('RUNTIME_RECEIPT='+json.dumps({'home':[p.name for p in pathlib.Path('/home/sandbox').iterdir()]}))"
  inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-process-recovery-a583d45');import worker; print(json.dumps(worker.execute("+repr(self.job['runtime_id'])+","+repr(guest)+",'probe',b'')))"
  ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
  names=json.loads(subprocess.check_output(ssh+['python3 -c '+shlex.quote(inner)],timeout=45))['home']
  additions=[]
  for name in names:
   assert '/' not in name and name not in ('.','..')
   if not any(e['path']==name or e['path'].startswith(name+'/') for e in manifest['entries']):
    # Preserve newly created owner data too. The guest still validates every
    # path, link and complete home inventory under its quiescence lock.
    manifest['entries'].append(dict(path=name,disposition='preserve'));additions.append(name)
  save('current-home-inventory.json',{'preserved_additions':additions})
  return manifest
class LocalWorker(transport.Worker):
 def fetch(self,role,receipt):
  path=P(receipt['local_path']);assert path==source_worker.root/(role+'.zip') and not path.is_symlink()
  assert path.stat().st_size==receipt['archive_bytes'] and transport.digest(path)==receipt['sha256'];return path
 def application_checks(self):
  state=self.control('GET','/status');assert not state['active_task']
  restarts={p['name']:p['restarts'] for p in state['processes']}
  headers={**self.headers,'Host':self.headers['Host'].replace('3031-',str(self.job['web_port'])+'-',1)}
  queue=assets.entries(self.http('GET','/',headers=headers,timeout=15));assert queue,'No Vite entries'
  seen=set();total=0;began=time.monotonic()
  while queue:
   path=queue.pop(0)
   if path in seen:continue
   seen.add(path);assert len(seen)<=512
   data=self.http('GET',path,headers=headers,timeout=45);total+=len(data);assert total<=64*1024**2
   queue.extend(p for p in assets.imports(path,data) if p not in seen)
  after=self.control('GET','/status')
  assert not after['active_task'] and all(p['running'] and p['restarts']==restarts[p['name']] for p in after['processes'])
  memory=assets.guest_memory(self.job['runtime_id']);assert memory['oom_kill']==0,'Guest OOM during compilation'
  save('module-health-'+str(time.time_ns())+'.json',{'passed':True,'memory':memory,'modules':len(seen),'bytes':total,'seconds':time.monotonic()-began,'model_calls':False})
 def verify(self):
  proof=super().verify();self.application_checks();return proof
batch_scope=None;inherited_locks=None
if os.environ.get('BAARCHA_VPS_BATCH_SCOPE'):
 batch_path=P(os.environ['BAARCHA_VPS_BATCH_SCOPE'])
 assert batch_path.parent.parent==root and batch_path.name=='scope.json'
 batch_scope=b.strict(b.trusted(batch_path))
 inherited_locks=json.loads(os.environ['BAARCHA_VPS_BATCH_LOCK_FDS'])
 assert batch_scope['parent_pid']==os.getppid() and batch_scope['concurrency'] in (2,4)
 assert any(e['sandbox_id']==sid and job.name in e['journals'] for e in batch_scope['selected'])
 assert len(inherited_locks)==4 and all(type(fd) is int and fd>2 for fd in inherited_locks)
@contextlib.contextmanager
def wait_for_operator():
 deadline=time.monotonic()+1800
 def permitted():
  barrier=root/'restore-barrier.json'
  if not barrier.exists():return True
  value=json.loads(barrier.read_text());assert value['purpose']=='reviewed-restore-barrier'
  return sid in value['allowed_sandboxes']
 while True:
  assert time.monotonic()<deadline,'Operator work or barrier still active; restore postponed'
  if not permitted():time.sleep(2);continue
  stack=contextlib.ExitStack()
  try:stack.enter_context(b.locked(inherited_locks))
  except BlockingIOError:
   stack.close();assert time.monotonic()<deadline,'Operator locks busy; restore postponed';time.sleep(2)
  else:
   if permitted():break
   stack.close();time.sleep(2)
 with stack:yield
with wait_for_operator():
 job.mkdir(mode=0o700,parents=True,exist_ok=False)
 with account_maintenance([sid],job):
  try:
   row=rows("select s.status,s.web_port,b.runtime_id,b.template_id,b.config_revision,p.worker_id,p.charged,p.state from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission p on p.runtime_id=b.runtime_id where s.id=? and p.state<>'deleted'",(sid,))[0]
   assert row['status']=='stopped' and row['template_id']=='tpl-78e4edb3d629465e9d8372c1' and row['worker_id']=='vps' and row['charged']==0 and row['state']=='released'
   tasks=[r['task_id'] for r in rows('select task_id from task where sandbox_id=? order by task_id',(sid,))]
   assert not rows("select task_id from task where status in ('running','queued')")
   open_journals=rows("select id from cube_relocation where phase='fenced'")
   assert not open_journals or (batch_scope and all(r['id'] in {j for e in batch_scope['selected'] for j in e['journals']} for r in open_journals)),'Unrelated relocation in progress'
   previous=[]
   for path in (root/'recovery-moves').glob('*/worker-job.PRIVATE.json'):
    old=json.loads(path.read_text())
    if old['sandbox_id']==sid and old['runtime_id']==row['runtime_id'] and (path.parent/'complete.json').exists():previous.append(old)
   if len(previous)==1:
    home=previous[0]['source']['home_manifest']
   else:
    assert not previous,'Ambiguous prior owner manifest'
    reviewed=b.strict(b.trusted(root/'reprofile-owner-manifests'/(sid+'.json')))
    assert reviewed['sandbox_id']==sid and reviewed['runtime_id']==row['runtime_id']
    assert reviewed['backup_verified'] and reviewed['same_owner_only']
    home=reviewed['home_manifest']
   if '.bash_logout' not in [e['path'] for e in home['entries']]:home['entries'].append(dict(path='.bash_logout',disposition='preserve'))
   if not any(e['path']=='.cache' or e['path'].startswith('.cache/') for e in home['entries']):home['entries'].append(dict(path='.cache',disposition='preserve'))
   origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
   def source_api(action):
    with lifecycle():
     status,_=copy.api('POST','/v1/sandboxes/'+sid+'/'+action);assert status==200
   save('source-before.json',row)
   source_api('start')
   source_worker=SourceWorker({'worker':'vps','id':job.name+'-source','sandbox_id':sid,'runtime_id':row['runtime_id'],'headers':headers,'home_manifest':home,'task_ids':tasks})
   try:
    source=source_worker.export()
    for role,v in source['artifacts'].items():v['local_path']=str(source_worker.root/(role+'.zip'))
    save('export-result.PRIVATE.json',source)
   finally:
    if source is None:source_worker.control('POST','/workspace/resume')
    source_api('stop')
   assert tasks==[r['task_id'] for r in rows('select task_id from task where sandbox_id=? order by task_id',(sid,))]
   save('scope.json',{'sandbox_id':sid,'source_worker':'vps','target_worker':'vps','fresh_current_export':True,'at':time.time()})
   try:
    cli('fence',SandboxID=sid,ExpectedRuntime=source['runtime_id'],TargetWorker='vps',TargetTemplate=templates[profile])
   except BaseException:
    # Failed fencing must not leave the still-canonical source quiesced. Never
    # reopen a source if an ambiguous CLI result actually installed the fence.
    if not rows('select id from cube_relocation where id=?',(job.name,)) and rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[{'runtime_id':source['runtime_id']}]:
     source_api('start');source_worker.control('POST','/workspace/resume');source_api('stop')
    raise
   cli('create')
   target=json.loads((job/'target.PRIVATE.json').read_text());runtime=target['Runtime']['sandboxID'] if 'sandboxID' in target['Runtime'] else target['Runtime'].get('sandbox_id')
   assert runtime,'target provider identity missing'
   request={'worker':'vps','id':job.name,'sandbox_id':sid,'runtime_id':runtime,'headers':{'Host':'3031-'+runtime+'.'+target['Relocation']['Domain'],'Authorization':'Bearer '+target['supervisor_token'],'cube-traffic-access-token':target['traffic_access_token']},'source':source,'receipts':source['artifacts'],'env':target['Env'],'config_revision':row['config_revision'],'web_port':row['web_port'] or 3000}
   save('worker-job.PRIVATE.json',request)
   save('channel-request.PRIVATE.json',{'Action':'channel','ID':job.name,'Directory':str(job),'Migrations':str(migrations),'PackageDownloads':True})
   with (job/'channel-error.log').open('ab') as error:
    channel=subprocess.Popen([str(BIN),str(job/'channel-request.PRIVATE.json')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=error)
    try:
     assert select.select([channel.stdout],[],[],60)[0]
     channel_ready=json.loads(channel.stdout.readline());assert channel_ready.get('channel_ready') and channel_ready['outbound']=='npm-registry-only'
     canary=json.loads((root/'supervisor-canary-a583d45/passed.json').read_text());assert canary['passed'] and canary['revision']=='a583d45'
     ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
     update=subprocess.run(ssh+['python3','/opt/baarcha-vps-process-recovery-a583d45/worker.py','--container',runtime],capture_output=True,timeout=260)
     assert update.returncode==0,'Supervisor update did not complete'
     receipts=[json.loads(line) for line in update.stdout.splitlines()]
     assert len(receipts)==1 and receipts[0]['status'] in ('updated','current'),'Supervisor update requires reconciliation'
     save('supervisor-update.json',receipts[0])
     worker=LocalWorker(request);proof=worker.restore();save('verified.json',proof)
    finally:
     channel.stdin.close();channel.wait(timeout=30)
   cli('commit')
   env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
   import urllib.request
   def api(action):
    req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
    with lifecycle():
     with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
   api('start');api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began
   worker.http('GET','/',headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)},timeout=10)
   worker.application_checks()
   api('stop')
   assert rows("select worker_id,state,charged from cube_admission where runtime_id=?",(runtime,))==[{'worker_id':'vps','state':'released','charged':0}]
   result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':profile,'fresh_current_export':True,'same_project_identity':True,'wake_seconds':wake,'all_content_verified':True,'source_retained':True,'at':time.time()};save('complete.json',result);print(json.dumps(result),flush=True)
  except BaseException as error:
   save('failed.json',{'error':type(error).__name__,'reason':str(error)[:512],'line':traceback.extract_tb(error.__traceback__)[-1].lineno,'at':time.time(),'source_retained':True})
   # Only a proven guest OOM before routing changes permits one larger-profile
   # attempt. The unused target is discarded by the guarded CLI; the canonical
   # source is resumed and freshly exported again, preserving current data.
   if profile in ('balanced','standard') and 'worker' in globals() and (worker.root/'content-verified.json').exists() and rows('select phase from cube_relocation where id=?',(job.name,))==[{'phase':'fenced'}] and rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[{'runtime_id':source['runtime_id']}]:
    memory=assets.guest_memory(runtime);save('failed-guest-memory.json',memory)
    if memory['oom_kill']>0:
     next_profile='standard' if profile=='balanced' else 'large'
     cli('discard-target')
     source_api('start');source_worker.control('POST','/workspace/resume');source_api('stop')
     save('promotion.json',{'from':profile,'to':next_profile,'proven_oom_kills':memory['oom_kill'],'unused_target_discarded':True,'source_preserved':True})
     print(json.dumps({'stage':'memory-profile-promotion','sandbox_id':sid,'from':profile,'to':next_profile}),flush=True)
     for fd in inherited_locks or []:os.set_inheritable(fd,True)
     os.execve('/usr/bin/python3',['/usr/bin/python3',str(root/'reprofile-vps.py'),sid,attempt,next_profile],os.environ)
   raise
