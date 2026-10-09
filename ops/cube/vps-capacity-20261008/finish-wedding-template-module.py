"""Restore one missing template module only after matching the entire saved source.

No tenant-specific source is copied: the donor is the platform's tagged starter.
The retained backup and live files must match it before this one-file repair.
"""
import hashlib,importlib.util,json,os,pathlib,shlex,sqlite3,subprocess,sys,tarfile,time,zipfile
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
out=root/'wedding-template-module-repair-37'
sid='01M4DWDR4TQ4JRBTTTJMG5SB78';runtime='ce648b2c8a804ad2a0e989d61f0d351b'
donor='01M1HH5DT8FVCP5TNRESEDJBH6';backup=P('/var/backups/baarcha-vps-source/20261009T162045Z/sandboxes')
def module(name,path):
 spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
b=module('boot','/usr/local/libexec/baarcha-cube-boot-transition.py')
copy=module('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py')
assets=module('assets',root/'preview-assets.py')
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def rows(q,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(q,args)]
def saved(which):
 with tarfile.open(backup/which/'home.tar.gz') as t:
  return {m.name.removeprefix('./workspace/app/'):t.extractfile(m).read() for m in t if m.isfile() and (m.name.startswith('./workspace/app/src/') or m.name in ['./workspace/app/package.json','./workspace/app/BRAIN.md','./workspace/app/AGENTS.md'])}
def sha(data):return hashlib.sha256(data).hexdigest()
def manifest(path):
 with zipfile.ZipFile(path) as z:return {e.filename:(e.external_attr,e.file_size,sha(z.read(e.filename))) for e in z.infolist() if not e.filename.startswith('node_modules/.vite/')}
def api(action):
 code,body=copy.api('POST','/v1/sandboxes/'+sid+'/'+action)
 if code!=200:
  b.atomic(out/(action+'-error.PRIVATE.json'),b.encoded(body));raise RuntimeError(action+' HTTP '+str(code))
with b.locked():
 journal=root/'wedding-template-module-finish-41';journal.mkdir(mode=0o700)
 assert not (out/'complete.json').exists()
 before=manifest(out/'before.PRIVATE.zip');after=manifest(out/'after.PRIVATE.zip')
 path='src/data/wedding.ts';payload=json.loads((out/'repair.PRIVATE.json').read_text())
 assert set(after)-set(before)=={'src/data/',path} and not set(before)-set(after)
 assert all(after[n]==v for n,v in before.items() if n!='BRAIN.md')
 assert after[path][2]==payload['data_sha256'] and after['BRAIN.md'][2]==sha(payload['brain'].encode())
 types=json.loads((out/'types.json').read_text());assert all(c['exit']==0 for c in types['checks'])
 assert rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[{'runtime_id':runtime}]
 assert rows('select status from sandbox where id=?',(sid,))==[{'status':'stopped'}]
 history=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,));assert not history
 assert not rows("select task_id from task where status in ('running','queued')")
 started=False;quiesced=False
 with account_maintenance([sid],journal):
  try:
   started=True;api('start');origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
   worker=transport.Worker({'worker':'vps','id':journal.name,'sandbox_id':sid,'runtime_id':runtime,'headers':headers,'web_port':3000})
   assert not worker.control('GET','/status')['active_task']
   web={**headers,'Host':headers['Host'].replace('3031-','3000-',1)}
   quiesced=True;worker.control('POST','/workspace/quiesce')
   worker.http('GET','/export/private-workspace-v2',export=journal/'current.PRIVATE.zip')
   current=manifest(journal/'current.PRIVATE.zip');assert current==after,'User work changed after repair'
   worker.control('POST','/workspace/resume');quiesced=False
   def health():
    deadline=time.monotonic()+60
    while True:
     code,_=copy.request(*origin,'/',headers=web,timeout=5)
     if code==200:break
     assert code in (502,503) and time.monotonic()<deadline, 'Preview failed readiness: '+str(code)
     time.sleep(1)
    base,html=assets.page(worker.origin,web);static=base.rsplit('/',1)[0]+'/';queue=assets.entries(html,base,static);seen=set();total=0
    while queue:
     name=queue.pop(0)
     if name in seen:continue
     seen.add(name);assert len(seen)<=512
     data=worker.http('GET',name,headers=web,timeout=45);total+=len(data);assert total<=64*1024**2
     queue.extend(p for p in assets.imports(name,data,static) if p not in seen)
    memory=assets.guest_memory(runtime);assert memory['oom_kill']==0
    return {'modules':len(seen),'bytes':total,'guest_memory':memory,'new_oom_kills':0}
   first=health();api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;second=health()
   expected=json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text())['receipt']['sha256']
   update=subprocess.run(ssh+['python3','/opt/baarcha-vps-export-recovery-2c7e700/worker.py','--container',runtime],capture_output=True,timeout=460)
   b.atomic(journal/'supervisor.PRIVATE.log',update.stdout+update.stderr);assert update.returncode==0
   receipt=json.loads(update.stdout);assert receipt['sha256']==expected and receipt['status']=='current'
  finally:
   if quiesced:worker.control('POST','/workspace/resume')
   if started and rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==history:api('stop')
  assert rows('select s.status,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))==[{'status':'stopped','state':'released','charged':0}]
 result={'sandbox_id':sid,'runtime_id':runtime,'verification':'template-module-repair-full-wake-and-modules','wake_seconds':wake,'original_running_preserved':True,'supervisor_sha256':expected,**first}
 case=root/'fleet-wake-validation-01'/(sid+'-'+runtime);assert (case/'failed.json').exists() and not (case/'passed.json').exists()
 proof={'complete':True,'result':result,'module_checks':[first['modules'],second['modules']],'changed_files':['src/data/wedding.ts','BRAIN.md'],'new_directory':'src/data/','b200_contacted':False,'model_calls':False,'at':time.time()}
 b.atomic(journal/'complete.json',b.encoded(proof));b.atomic(out/'complete.json',b.encoded(proof))
 b.atomic(case/'source-repair.json',b.encoded({'proof':str(out/'complete.json'),'original_failure_retained':True}))
 b.atomic(case/'passed.json',b.encoded({'tasks':history,'result':result}))
 print(json.dumps({'complete':True,'modules':first['modules'],'wake_seconds':wake}),flush=True)
