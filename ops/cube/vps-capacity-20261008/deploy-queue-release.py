#!/usr/bin/env python3
"""Apply only the preview controller image/config, with exact-baseline rollback."""
import contextlib,fcntl,json,os,pathlib,sqlite3,subprocess,time,urllib.request,hashlib,importlib.util,shutil,signal
P=pathlib.Path;ROOT=P('/opt/baarcha/operations/vps-50-profiles-20261008/queue-release-d463b2d');os.umask(0o077)
FILES=[P('/opt/sandboxd/deploy-state/runtime-compose.json'),P('/opt/sandboxd/deploy-state/active-images.json')];STOP=P('/etc/baarcha-cube/worker-stop.json')
BASE='sha256:d042906709f5b6088a9535caee0f51d6980dd9f47c4cde97e00e92320175c69c'
LOCKS=['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']
def run(args):return subprocess.check_output(args,stderr=subprocess.STDOUT,timeout=180)
def inspect():return json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
def compose(*args):return run(['docker','compose','--project-directory','/opt/sandboxd/src','-f','/opt/sandboxd/src/docker-compose.yml','-f',str(FILES[0]),'-f',str(FILES[1]),*args])
def put(p,b):
 tmp=p.with_name(p.name+'.preview-new');tmp.write_bytes(b);tmp.chmod(0o600);os.replace(tmp,p)
def db():return sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=10)
def bindings():
 with contextlib.closing(db()) as c:return c.execute('select sandbox_id,runtime_id from runtime_binding order by sandbox_id').fetchall()
spec=importlib.util.spec_from_file_location('maintenance','/usr/local/libexec/baarcha-cube-maintenance.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);b=m.b

def route(value):
 req=urllib.request.Request('http://127.0.0.1:2019/load',data=json.dumps(value).encode(),headers={'Content-Type':'application/json'},method='POST')
 with urllib.request.urlopen(req,timeout=10) as response:assert response.status==200
 assert b.strict(b.http('/config/',2019))==value

def quiet():
 with contextlib.closing(db()) as c:
  assert c.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0,'Active task; release postponed'
  assert c.execute("select count(*) from cube_admission where state='pending'").fetchone()[0]==0,'Provider operation in progress'

def ready():
 for _ in range(150):
  try:
   with urllib.request.urlopen('http://127.0.0.1:9090/readyz',timeout=5) as r:
    if r.status==200:return
  except Exception:time.sleep(1)
 raise RuntimeError('controller readiness failed')
with contextlib.ExitStack() as stack:
 for path in LOCKS:
  f=stack.enter_context(open(path,'a+'));fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
 assert not (ROOT/'deployed.json').exists(),'Release already applied'
 before=inspect();assert before['Image']==BASE,'Controller baseline changed'
 quiet()
 with contextlib.closing(db()) as c:
  assert c.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0,'Active task: wait for completion'
  assert c.execute("select count(*) from cube_admission where state='pending'").fetchone()[0]==0,'Provider operation in progress'
  with sqlite3.connect(ROOT/'before-preview.PRIVATE.sqlite') as backup:c.backup(backup)
 before_bindings=bindings();original={str(p):p.read_bytes() for p in [*FILES,STOP]}
 for p in [*FILES,STOP]:put(ROOT/(p.name+'.before'),original[str(p)])
 oldrender=json.loads(compose('config','--format','json'));candidate=(ROOT/'image.id').read_text().strip()
 assert candidate.startswith('sha256:')
 oldenv=dict(v.split('=',1) for v in before['Config']['Env']);fleet=json.loads(oldenv['SANDBOXD_CUBE_FLEET'])
 assert {w['id'] for w in fleet['workers']}=={'vps','b200-01'}
 for worker in fleet['workers']:worker['draining']=worker['id']!='vps'
 fleet_raw=oldenv['SANDBOXD_CUBE_FLEET']
 assert all(w['draining'] for w in json.loads(fleet_raw)['workers'] if w['id']!='vps')
 assert int(oldenv.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0,'Do not change a live coding limit'
 online=b.strict(b.http('/config/',2019));scope=json.loads(pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008/vps-resize-plan.PRIVATE.json').read_text())['routing'];scope['online_sha256']=hashlib.sha256(json.dumps(online,sort_keys=True,separators=(',',':')).encode()).hexdigest();routes=m.routing_variants(online,scope)
 put(ROOT/'routing-before.PRIVATE.json',json.dumps(online).encode())
 helpers={pathlib.Path('/usr/local/libexec/baarcha-'+command):(ROOT/command) for command in ['cube-worker-start','cube-worker-stop']}
 for destination in helpers:shutil.copy2(destination,ROOT/(destination.name+'.before'))
 timers={name:run(['systemctl','show',name,'-p','ActiveState','--value']).decode().strip() for name in b.TIMERS}
 put(ROOT/'timers-before.json',json.dumps(timers).encode())
 recreated=False
 def interrupted(*args):raise SystemExit('release interrupted')
 signal.signal(signal.SIGTERM,interrupted)
 try:
  route(routes['offline']);run(['systemctl','stop',*b.TIMERS]);time.sleep(3);quiet()
  for p in FILES:
   value=json.loads(original[str(p)]);svc=value['services']['sandboxd'];svc['image']=candidate;svc.setdefault('environment',{})['SANDBOXD_CUBE_FLEET']=fleet_raw
   put(p,json.dumps(value).encode())
  newrender=json.loads(compose('config','--format','json'));expected=json.loads(json.dumps(oldrender));expected['services']['sandboxd']['image']=candidate;expected['services']['sandboxd']['environment']['SANDBOXD_CUBE_FLEET']=fleet_raw
  assert newrender==expected,'Unexpected Compose changes'
  recreated=True
  compose('up','-d','--no-deps','--no-build','--pull','never','sandboxd')
  compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy')
  ready();after=inspect();assert after['Image']==candidate and after['State']['Running']
  for destination,source in helpers.items():run(['install','-m','755',str(source),str(destination)])
  stop=json.loads(original[str(STOP)]);assert stop['controller_id']==before['Id'];stop['controller_id']=after['Id'];put(STOP,json.dumps(stop).encode())
  assert bindings()==before_bindings,'Runtime bindings changed during rollout'
  observed=json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe']));assert observed['consistent']
  for name,state in timers.items():
   if state=='active':run(['systemctl','start',name])
  route(online)
  result={'coding_queue_enabled':False,'deployed':True,'controller':after['Id'],'image':candidate,'previous_image':BASE,'bindings_preserved':len(before_bindings),'new_allocations':'vps-only','b200_bindings_retained_for_recovery':True}
  put(ROOT/'deployed.json',json.dumps(result).encode());print(json.dumps(result))
 except BaseException:
  for destination in helpers:run(['install','-m','755',str(ROOT/(destination.name+'.before')),str(destination)])
  for p in FILES:put(p,original[str(p)])
  if recreated:
   compose('up','-d','--no-deps','--no-build','--pull','never','sandboxd')
   compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy');ready()
  stop=json.loads(original[str(STOP)]);stop['controller_id']=inspect()['Id'];put(STOP,json.dumps(stop).encode())
  for name,state in timers.items():
   if state=='active':run(['systemctl','start',name])
  route(online)
  print('Controller rolled back to previous image/config')
  raise
