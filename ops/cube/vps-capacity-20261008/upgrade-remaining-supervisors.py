"""Finish the reviewed supervisor rollout on canonical VPS apps, preserving state."""
import importlib.util,json,os,pathlib,sqlite3,subprocess,time
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'supervisor-rollout-2c7e700'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
import sys;sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
resume=sys.argv[1:]==['--resume-reviewed-binary'];assert not sys.argv[1:] or resume
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def rows(query,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(query,args)]
with b.locked():
 assert json.loads((root/'finish-vps-recovery-20/complete.json').read_text())['complete']
 canary=json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text());assert canary['passed'];expected=canary['receipt']['sha256']
 assert not rows("select id from cube_relocation where phase='fenced'") and not rows("select admission_key from cube_admission where state='pending'")
 assert not rows("select task_id from task where status in ('running','queued')")
 scope=rows("select s.id,s.status,s.web_port,b.runtime_id,a.worker_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id order by s.id")
 assert len(scope)>=135 and all(r['worker_id']=='vps' and (r['status'],r['state'],r['charged']) in [('stopped','released',0),('running','active',1)] for r in scope)
 if resume:
  assert out.is_dir() and not (out/'complete.json').exists()
  original_scope=json.loads((out/'scope.json').read_text())
  def identities(data):return [{k:v for k,v in r.items() if k not in ('status','state','charged')} for r in data]
  assert identities(original_scope)==identities(scope),'Fleet identity changed; review before resuming'
  # Normal visitor wakes/idle stops may occur during review. Preserve the fresh
  # state, while requiring unchanged binding, profile and task identities.
  b.atomic(out/('resume-scope-'+str(time.time_ns())+'.json'),b.encoded(scope))
  approval=json.loads((root/'review-ea25000/complete.json').read_text());assert approval['reviewed'] and approval['normalized_reproduced_sha256']==approval['approved_previous_sha256'] and approval['all_other_bytes_identical']
  # Preserve the failed attempt and require its exact non-mutating rejection.
  rejected=out/'01M2P1KJ8086W06ANFAV50KA93'/'update.PRIVATE.log'
  receipt=json.loads(rejected.read_text());assert receipt['status']=='unreviewed_binary' and receipt['sha256']==approval['approved_previous_sha256']
  backup=json.loads(P('/var/backups/baarcha-vps-source/latest.json').read_text());assert backup['verified']
  with sqlite3.connect('file:/var/backups/baarcha-vps-source/'+backup['generation']+'/controller.PRIVATE.sqlite?mode=ro',uri=True) as db:
   history=[tuple(r) for r in db.execute('select task_id,sandbox_id,status from task order by task_id')]
  assert history==[tuple(r.values()) for r in rows('select task_id,sandbox_id,status from task order by task_id')],'User work changed; review before resuming'
  previous=json.loads((out/'progress.json').read_text());assert len(previous)==43
  b.atomic(out/('resume-'+str(time.time_ns())+'.json'),b.encoded({'approved_previous_sha256':approval['approved_previous_sha256'],'completed_checks':len(previous),'at':time.time()}))
 else:
  out.mkdir(mode=0o700);b.atomic(out/'scope.json',b.encoded(scope));previous=[]
 verified={canary['runtime_id']}
 paths=[root/'exited-recovery-01/supervisor-update.json',root/'exited-recovery-02/supervisor-update.json']+list((root/'recovery-moves').glob('*/supervisor-update.json'))+list(root.glob('real-preview-density-50-balanced*/*-supervisor.json'))
 for path in paths:
  receipt=json.loads(path.read_text())
  if receipt.get('sha256')==expected and receipt.get('status') in ('updated','current'):verified.add(receipt['runtime_id'])
 for row in previous:
  if row['status']=='updated':
   receipt=json.loads((out/row['sandbox_id']/'updated.json').read_text());assert receipt['runtime_id']==row['runtime_id'] and receipt['sha256']==expected
   verified.add(row['runtime_id'])
 results=[]
 for row in scope:
  sid=row['id'];runtime=row['runtime_id']
  if runtime in verified:
   results.append({'sandbox_id':sid,'runtime_id':runtime,'status':'verified_previous_receipt'});continue
  assert not rows("select task_id from task where status in ('running','queued')"),'User task started; stop rollout'
  assert rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[{'runtime_id':runtime}]
  job=out/sid
  if job.exists():
   assert resume and sid=='01M2P1KJ8086W06ANFAV50KA93'
   job=job/('reviewed-retry-'+str(time.time_ns()))
  job.mkdir(mode=0o700)
  tasks=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))
  own_activity=None;started=False
  with account_maintenance([sid],job):
   def api(action):
    code,_=copy.api('POST','/v1/sandboxes/'+sid+'/'+action);assert code==200
   try:
    if row['status']=='stopped':
     started=True;api('start');own_activity=rows('select last_active_at from sandbox where id=?',(sid,))[0]['last_active_at']
    origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
    worker=transport.Worker({'worker':'vps','id':'supervisor-rollout-'+sid,'sandbox_id':sid,'runtime_id':runtime,'headers':headers,'web_port':row['web_port'] or 3000})
    def healthy():
     deadline=time.monotonic()+60
     while True:
      try:
       status=worker.control('GET','/status');assert not status['active_task'] and all(p['running'] for p in status['processes'])
       assets.page(worker.origin,{**headers,'Host':headers['Host'].replace('3031-',str(row['web_port'] or 3000)+'-',1)},timeout=10);return
      except (OSError,RuntimeError,AssertionError):assert time.monotonic()<deadline;time.sleep(1)
    healthy()
    update=subprocess.run(ssh+['python3','/opt/baarcha-vps-export-recovery-2c7e700/worker.py','--container',runtime],capture_output=True,timeout=460)
    b.atomic(job/'update.PRIVATE.log',update.stdout+update.stderr)
    receipts=[json.loads(line) for line in update.stdout.splitlines()]
    assert update.returncode==0 and len(receipts)==1 and receipts[0]['status'] in ('updated','current') and receipts[0]['sha256']==expected
    healthy();b.atomic(job/'updated.json',b.encoded(receipts[0]))
   finally:
    unchanged=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==tasks
    assert unchanged,'Task activity changed; preserve runtime for review'
    activity=rows('select last_active_at from sandbox where id=?',(sid,))[0]['last_active_at']
    if started and (own_activity is None or activity<=own_activity):api('stop')
  results.append({'sandbox_id':sid,'runtime_id':runtime,'status':receipts[0]['status']})
  b.atomic(out/'progress.json',b.encoded(results));print(json.dumps({'verified':len(results),'total':len(scope)}),flush=True)
 b.atomic(out/'complete.json',b.encoded({'complete':True,'revision':'2c7e700','sandboxes':len(scope),'results':results,'model_calls':False,'b200_contacted':False}))

# Old checkpoints need a live wake check even when the supervisor binary was current.
subprocess.run(['/usr/bin/python3',str(root/'verify-vps-fleet-wakes.py')],check=True,timeout=5400)
subprocess.run(['/usr/bin/python3',str(root/'refresh-source-backup.py')],check=True,timeout=7*3600)
