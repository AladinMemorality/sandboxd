"""Reprofile a stopped VPS app using a fresh verified export of its current state."""
import contextlib,fcntl,importlib.util,json,os,pathlib,select,shutil,shlex,sqlite3,subprocess,sys,time,traceback
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sid=sys.argv[1];assert len(sid)==26 and sid.isalnum()
profile='balanced'
receipt=json.loads((root/'balanced-template-a583d45.json').read_text())
deployed=json.loads((root/'balanced-release-a583d45/deployed.json').read_text())
assert receipt['ready'] and receipt['worker']=='vps' and receipt['memory_mb']==768
assert deployed['deployed'] and deployed['template_id']==receipt['template_id']
templates={profile:receipt['template_id']}
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
job=root/'recovery-moves'/('vps-reprofile-'+sid.lower()+'-768-'+attempt)
BIN=root/'cube-relocate-reprofile';migrations=root/'queue-release-d463b2d/source/control-plane/migrations'
def save(name,value):b.atomic(job/name,b.encoded(value))
def rows(query,args=()):
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=10)) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(query,args)]
def cli(action,**extra):
 request={'Action':action,'ID':job.name,'Directory':str(job),'Migrations':str(migrations),**extra}
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
  save('module-health-'+str(time.time_ns())+'.json',{'passed':True,'modules':len(seen),'bytes':total,'seconds':time.monotonic()-began,'model_calls':False})
 def verify(self):
  proof=super().verify();self.application_checks();return proof
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
  try:stack.enter_context(b.locked())
  except BlockingIOError:
   stack.close();assert time.monotonic()<deadline,'Operator locks busy; restore postponed';time.sleep(2)
  else:
   if permitted():break
   stack.close();time.sleep(2)
 with stack:yield
with wait_for_operator():
 assert sid=='01M16MV7KZSF3YNAJ1VKWKYED5' and attempt=='01'
 assert json.loads((job/'failed.json').read_text())['error']=='HTTPFailure'
 assert not (job/'parser-resume-intent.json').exists()
 assert rows('select phase from cube_relocation where id=?',(job.name,))==[{'phase':'fenced'}]
 request=json.loads((job/'worker-job.PRIVATE.json').read_text());runtime=request['runtime_id']
 worker=LocalWorker(request)
 proof=json.loads((worker.root/'verified.json').read_text())
 assert proof['RuntimeID']==runtime and proof['RelocationID']==job.name and proof['ApplicationReady'] and proof['WorkspaceVerified'] and proof['HomeVerified'] and proof['HistoryVerified']
 save('parser-resume-intent.json',{'at':time.time(),'module_parser':'acorn-8.15.0'})
 try:
  worker.application_checks();save('verified.json',proof)
  cli('commit')
  env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
  import urllib.request
  def api(action):
   req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
   with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
  api('start');api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began
  worker.http('GET','/',headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)},timeout=10)
  worker.application_checks()
  api('stop')
  assert rows("select worker_id,state,charged from cube_admission where runtime_id=?",(runtime,))==[{'worker_id':'vps','state':'released','charged':0}]
  result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':profile,'fresh_current_export':True,'same_project_identity':True,'wake_seconds':wake,'all_content_verified':True,'source_retained':True,'at':time.time()};save('complete.json',result);print(json.dumps(result),flush=True)
 except BaseException as error:
  save('parser-resume-failed.json',{'error':type(error).__name__,'reason':str(error)[:512],'at':time.time()});raise
