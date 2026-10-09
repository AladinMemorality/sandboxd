"""Exercise full memory checkpoints on the restored app without model requests."""
import contextlib,importlib.util,json,os,pathlib,sqlite3,subprocess,sys,time,urllib.request
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
sid='01M2AR44CA9AW2FEFA44KVEAGQ';rid='e3a26535a2874c77a799e45f40158cd1'
out=root/'full-pause-canary-01';job=root/'recovery-moves/vps-restore-01m2ar44ca9aw2fefa44kveagq-panic2a'
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
from maintenance_account import account_maintenance
from migration_lifecycle import lifecycle
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def rows(q,args=()):
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:return db.execute(q,args).fetchall()
def save(name,v):b.atomic(out/name,b.encoded(v))
def native_log(offsets=None):
 code='''import pathlib,json
old=OFFSETS;rid=RID;result={};matches=0
for p in pathlib.Path('/data/log/Cubelet').glob('*.log'):
 st=p.stat();result[p.name]={'inode':st.st_ino,'size':st.st_size}
 if old is not None:
  before=old[p.name];assert before['inode']==st.st_ino and before['size']<=st.st_size
  with p.open('rb') as f:f.seek(before['size']);data=f.read()
  for line in data.splitlines():
   if rid.encode() in line and b'PauseToSnapshot destination=' in line:
    assert b'snapshot_type=full' in line;matches+=1
print(json.dumps({'offsets':result,'full_pause_records':matches}))
'''.replace('OFFSETS',repr(offsets)).replace('RID',repr(rid))
 return json.loads(subprocess.check_output(SSH+['python3 -'],input=code.encode(),timeout=30))
with b.locked():
 assert json.loads((root/'full-pause-release-20261009/deployed.json').read_text())['deployed']
 assert not rows("select task_id from task where status in ('running','queued')")
 assert rows('select s.status,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))==[('stopped',rid,'released',0)]
 out.mkdir(mode=0o700);before=native_log();save('log-start.json',before)
 request=json.loads((job/'worker-job.PRIVATE.json').read_text());assert request['runtime_id']==rid;worker=transport.Worker(request)
 x=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];env=dict(v.split('=',1) for v in x['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
 def api(action):
  req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
  with lifecycle():
   with urllib.request.urlopen(req,timeout=180) as r:assert r.status==200;r.read()
 def warm():
  status=worker.control('GET','/status');assert not status['active_task'];restarts={p['name']:p['restarts'] for p in status['processes']}
  headers={**worker.headers,'Host':worker.headers['Host'].replace('3031-',str(request['web_port'])+'-',1)}
  base,html=assets.page(worker.origin,headers);static=base.rsplit('/',1)[0]+'/';queue=assets.entries(html,base,static);assert queue;seen=set();total=0
  while queue:
   path=queue.pop(0)
   if path in seen:continue
   seen.add(path);assert len(seen)<=512;data=worker.http('GET',path,headers=headers,timeout=45);total+=len(data);assert total<=64*1024**2
   queue.extend(p for p in assets.imports(path,data,static) if p not in seen)
  after=worker.control('GET','/status');assert not after['active_task'] and all(p['running'] and p['restarts']==restarts[p['name']] for p in after['processes'])
  memory=assets.guest_memory(rid);assert memory['oom_kill']==0
  return {'modules':len(seen),'bytes':total,'memory':memory}
 with account_maintenance([sid],out):
  results=[]
  try:
   # First wake consumes the pre-change checkpoint; every subsequent wake
   # must consume a full checkpoint observed in the native worker log.
   api('start');warm();api('stop')
   for cycle in range(3):
    assert not rows("select task_id from task where status in ('running','queued')"),'User work started; halt canary'
    began=time.monotonic();api('start');elapsed=time.monotonic()-began;health=warm();api('stop')
    assert rows('select s.status,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))==[('stopped','released',0)]
    results.append({'cycle':cycle+1,'wake_seconds':elapsed,**health});save('progress.json',results)
   after=native_log(before['offsets']);assert after['full_pause_records']>=4
   result={'passed':True,'sandbox_id':sid,'runtime_id':rid,'cycles':results,'full_pause_records':after['full_pause_records'],'model_calls':False,'b200_contacted':False,'at':time.time()};save('complete.json',result);print(json.dumps(result))
  except BaseException as error:
   save('failed.json',{'type':type(error).__name__,'reason':str(error)[:300],'at':time.time()});raise
