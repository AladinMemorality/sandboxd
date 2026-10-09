"""Verify published source on both the starter and repaired remix before activation."""
import contextlib,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,sys,time,zipfile
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');release=root/'source-data-release-3b1a6f0'
def module(name,path):
 spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
b=module('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');copy=module('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');assets=module('assets',root/'preview-assets.py')
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def rows(q,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(q,args)]
deadline=time.monotonic()+14400
for unit,proof in [('baarcha-vps-source-data-stage-53',release/'supervisor-staged.json'),('baarcha-vps-source-data-deploy-52',release/'deployed.json')]:
 while True:
  state=subprocess.check_output(['systemctl','show',unit,'-p','ActiveState','--value'],text=True).strip()
  assert state!='failed' and time.monotonic()<deadline,'Review preceding operation: '+unit
  if state=='inactive':break
  time.sleep(5)
 assert proof.exists() and subprocess.check_output(['systemctl','show',unit,'-p','Result','--value'],text=True).strip()=='success'
with b.locked():
 out=root/'source-data-supervisor-canary-54';out.mkdir(mode=0o700)
 expected=json.loads((release/'supervisor-release.json').read_text())['sha256'];results=[]
 for sid in ['01M1HH5DT8FVCP5TNRESEDJBH6','01M4DWDR4TQ4JRBTTTJMG5SB78']:
  job=out/sid;job.mkdir(mode=0o700)
  row=rows('select s.status,b.runtime_id,a.worker_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))[0]
  assert row['worker_id']=='vps' and (row['status'],row['state'],row['charged'])==('stopped','released',0)
  runtime=row['runtime_id'];history=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,));assert not any(t['status'] in ('running','queued') for t in history)
  started=False
  with account_maintenance([sid],job):
   def api(action):
    code,body=copy.api('POST','/v1/sandboxes/'+sid+'/'+action)
    if code!=200:
     b.atomic(job/(action+'-error.PRIVATE.json'),b.encoded(body));raise RuntimeError(action+' HTTP '+str(code))
   try:
    started=True;api('start');origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
    worker=transport.Worker({'worker':'vps','id':job.name,'sandbox_id':sid,'runtime_id':runtime,'headers':headers,'web_port':3000})
    update=subprocess.run(ssh+['python3','/opt/baarcha-vps-source-data-3b1a6f0/worker.py','--container',runtime],capture_output=True,timeout=460)
    b.atomic(job/'update.PRIVATE.log',update.stdout+update.stderr);assert update.returncode==0
    receipt=json.loads(update.stdout);assert receipt['sha256']==expected and receipt['status']=='updated' and receipt['config_preserved']
    b.atomic(job/'updated.json',b.encoded(receipt))
    web={**headers,'Host':headers['Host'].replace('3031-','3000-',1)}
    def health():
     ready=time.monotonic()+60
     while True:
      code,_=copy.request(*origin,'/',headers=web,timeout=5)
      if code==200:break
      assert code in (502,503) and time.monotonic()<ready;time.sleep(1)
     status=worker.control('GET','/status');assert not status['active_task'] and all(p['running'] for p in status['processes'])
     base,html=assets.page(worker.origin,web);static=base.rsplit('/',1)[0]+'/';queue=assets.entries(html,base,static);seen=set();total=0
     while queue:
      name=queue.pop(0)
      if name in seen:continue
      seen.add(name);assert len(seen)<=512
      data=worker.http('GET',name,headers=web,timeout=45);total+=len(data);assert total<=64*1024**2
      queue.extend(p for p in assets.imports(name,data,static) if p not in seen)
     return {'modules':len(seen),'bytes':total}
    first=health()
    worker.http('GET','/export/source',export=job/'published-source.PRIVATE.zip')
    with zipfile.ZipFile(job/'published-source.PRIVATE.zip') as z:
     names=z.namelist();data=z.read('src/data/wedding.ts')
     assert hashlib.sha256(data).hexdigest()=='d450e45fd957b2d4c4ee91b70340c82ebf4e58af6e3c459d1021d232bddc713b'
     assert not any(n.startswith(('.env','.git/','.runtimed/','node_modules/','data/','private/')) for n in names)
    api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;second=health()
    result={'sandbox_id':sid,'runtime_id':runtime,'sha256':expected,'exported_source_module_sha256':hashlib.sha256(data).hexdigest(),'exported_files':len(names),'module_checks':[first['modules'],second['modules']],'wake_seconds':wake,'config_preserved':True,'passed':True}
   finally:
    assert rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==history,'User task changed; preserve runtime'
    if started:api('stop')
   assert rows('select status from sandbox where id=?',(sid,))==[{'status':'stopped'}]
  b.atomic(job/'passed.json',b.encoded(result));results.append(result)
 proof={'passed':True,'revision':'3b1a6f0','sha256':expected,'results':results,'b200_contacted':False,'model_calls':False,'at':time.time()}
 b.atomic(out/'complete.json',b.encoded(proof));print(json.dumps(proof),flush=True)
