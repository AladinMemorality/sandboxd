"""Restore one stopped B200 project from VPS-local verified artifacts only."""
import contextlib,fcntl,importlib.util,json,os,pathlib,select,shutil,sqlite3,subprocess,sys,time,traceback
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sid=sys.argv[1];assert len(sid)==26 and sid.isalnum()
profile=sys.argv[2] if len(sys.argv)>2 else 'small'
templates={'small':'tpl-78e4edb3d629465e9d8372c1','standard':'tpl-86350411460a47db8ffe6ff5','large':'tpl-5abec4cb4fcc41cc8e611f69'}
if profile=='owner-data':
 receipt=json.loads((root/'owner-data-template.json').read_text())
 deployed=json.loads((root/'owner-data-release-87a99c7/deployed.json').read_text())
 assert receipt['ready'] and receipt['worker']=='vps' and receipt['memory_mb']==1024
 assert deployed['deployed'] and deployed['template_id']==receipt['template_id']
 templates[profile]=receipt['template_id']
assert profile in templates
attempt=sys.argv[3] if len(sys.argv)>3 else ''
assert not attempt or (attempt.isalnum() and len(attempt)<=16)
prepared=root/'recovery-prepared-canonical'/sid;source=json.loads((prepared/'export-result.PRIVATE.json').read_text())
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
subprocess.run([str(root/'artifact-validator'),str(prepared)],check=True,capture_output=True,timeout=180)
job=root/'recovery-moves'/('vps-restore-'+sid.lower()+('-'+attempt if attempt else ''))
BIN=root/'cube-relocate-package-recovery';migrations=root/'queue-release-d463b2d/source/control-plane/migrations'
def save(name,value):b.atomic(job/name,b.encoded(value))
def rows(query,args=()):
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=10)) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(query,args)]
def cli(action,**extra):
 request={'Action':action,'ID':job.name,'Directory':str(job),'Migrations':str(migrations),**extra}
 p=subprocess.run([str(BIN)],input=json.dumps(request).encode(),capture_output=True,timeout=200)
 if p.returncode:save('cli-'+action+'-failed.json',{'exit_code':p.returncode});raise RuntimeError('relocation '+action+' refused')
 return json.loads(p.stdout)
class LocalWorker(transport.Worker):
 def fetch(self,role,receipt):
  path=P(receipt['local_path']);assert path==prepared/(role+'.zip') and not path.is_symlink()
  assert path.stat().st_size==receipt['archive_bytes'] and transport.digest(path)==receipt['sha256'];return path
 def application_checks(self):
  checks={'01M1HJ4EXF1GS6GE3BS9G3ANF3':('gateway','/api/rules'),'01M3C9C0V0MQYNFTMCYS7CCNVC':('postgres','/api/health')}
  if sid not in checks:return
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
 try:
  assert sid=='01M2FS3WRK6FVDSG7VWC8QWSZN' and profile=='small'
  assert json.loads((job/'failed.json').read_text())['reason']=='relocation create refused'
  intent=json.loads((job/'create-intent.PRIVATE.json').read_text());fence=intent['Relocation']
  assert rows('select phase from cube_relocation where id=?',(job.name,))==[{'phase':'fenced'}]
  assert rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[{'runtime_id':source['runtime_id']}]
  assert not (job/'retry-intent.PRIVATE.json').exists()
  # Native scheduler logs prove rejection before allocation, matching operation.
  native_code="""import json,sys,pathlib
wanted=json.load(sys.stdin);requests=[];rejected=[]
for line in pathlib.Path('/data/log/CubeMaster/cubemaster-req.log').open():
 try:v=json.loads(line)
 except ValueError:continue
 if not v.get('@timestamp','').startswith('2026-10-08T23:19:08'):continue
 text=v.get('LogContent','')
 if text.startswith('CreateSandbox:'):
  data=json.loads(text.split(':',1)[1]);labels=data.get('labels',{})
  if labels.get('sandboxd_relocation_id')==wanted['RelocationID'] and labels.get('sandboxd_admission_operation')==wanted['OperationToken']:requests.append(data['requestID'])
 if text.startswith('CreateSandbox_rsp fail:'):
  data=json.loads(text.split(':',1)[1])
  if data['ret']['ret_code']==130597:rejected.append(data['RequestID'])
assert len(requests)==1 and requests[0] in rejected
wanted.update(RequestID=requests[0],HTTPStatus=500,FailureCode=130597)
print(json.dumps(wanted))
"""
  import shlex
  ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
  proof=json.loads(subprocess.check_output(ssh+['python3 -c '+shlex.quote(native_code)],input=json.dumps({'RelocationID':job.name,'OperationToken':intent['Admission']['Token']}).encode(),timeout=30))
  save('provider-rejection.json',proof)
  # Preserve the previous operator binary; only the tested offline tool changes.
  candidate=root/'relocation-retry-bd965af/cube-relocate';assert candidate.is_file()
  shutil.copy2(BIN,job/'operator-before');shutil.copy2(candidate,BIN.with_suffix('.new'));BIN.with_suffix('.new').chmod(0o755);os.replace(BIN.with_suffix('.new'),BIN)
  row={'config_revision':fence['ConfigRevision'],'web_port':rows('select web_port from sandbox where id=?',(sid,))[0]['web_port']}
  cli('retry-rejected-create')
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
    canary=json.loads((root/'supervisor-canary-6acbaeb/passed.json').read_text());assert canary['passed'] and canary['revision']=='6acbaeb'
    ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
    update=subprocess.run(ssh+['python3','/opt/baarcha-vps-process-recovery-6acbaeb/worker.py','--container',runtime],capture_output=True,timeout=260)
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
   with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
  api('start');api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began
  worker.http('GET','/',headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)},timeout=10)
  worker.application_checks()
  api('stop')
  assert rows("select worker_id,state,charged from cube_admission where runtime_id=?",(runtime,))==[{'worker_id':'vps','state':'released','charged':0}]
  result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':profile,'source_contacted':False,'same_project_identity':True,'wake_seconds':wake,'all_content_verified':True,'source_retained':True,'at':time.time()};save('complete.json',result);print(json.dumps(result),flush=True)
 except BaseException as error:
  save('failed.json',{'error':type(error).__name__,'reason':str(error)[:512],'line':traceback.extract_tb(error.__traceback__)[-1].lineno,'at':time.time(),'source_retained':True});raise
