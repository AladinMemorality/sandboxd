#!/usr/bin/env python3
"""Apply only the shared-design-skills controller image/config, with exact-baseline rollback."""
import contextlib,fcntl,json,os,pathlib,sqlite3,subprocess,time,urllib.request,hashlib,sys
P=pathlib.Path;ROOT=P('/opt/baarcha-bench/design-skills-20261001');os.umask(0o077)
FILES=[P('/opt/sandboxd/deploy-state/runtime-compose.json'),P('/opt/sandboxd/deploy-state/active-images.json')];STOP=P('/etc/baarcha-cube/worker-stop.json')
BASE='sha256:bfa876f6523eb676038dba1bd91f8e00d4c65aee6195940f38119501694c6f1a'
LOCKS=['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']
def run(args):return subprocess.check_output(args,stderr=subprocess.STDOUT)
def inspect():return json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
def compose(*args):return run(['docker','compose','--project-directory','/opt/sandboxd/src','-f','/opt/sandboxd/src/docker-compose.yml','-f',str(FILES[0]),'-f',str(FILES[1]),*args])
def put(p,b):
 tmp=p.with_name(p.name+'.preview-new');tmp.write_bytes(b);tmp.chmod(0o600);os.replace(tmp,p)
def db():return sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=10)
def bindings():
 with contextlib.closing(db()) as c:return c.execute('select sandbox_id,runtime_id from runtime_binding order by sandbox_id').fetchall()
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
 before=inspect();assert before['Image']==BASE,'Controller baseline changed'
 assert json.loads(STOP.read_bytes())['controller_id']==before['Id'],'Stop pin no longer matches current controller'
 candidate=(ROOT/'image.id').read_text().strip()
 assert candidate.startswith('sha256:') and candidate!=BASE
 assert json.loads(run(['docker','image','inspect',candidate]))[0]['Id']==candidate
 # Reject a wrong entrypoint before touching the live controller. This runs
 # without network, writable root, credentials or production data mounts.
 verifier='cube-design-verify-'+str(os.getpid())
 try:
  result=subprocess.run(['docker','run','--rm','--name',verifier,'--network=none','--read-only','--entrypoint','/usr/local/bin/cube-controller',candidate,'version'],capture_output=True,timeout=10)
  assert result.returncode==0 and result.stdout.strip()==('cube-controller dev ('+(ROOT/'build.commit').read_text().strip()+')').encode(),'Candidate is not the tested Cube controller build'
 finally:
  subprocess.run(['docker','rm','-f',verifier],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 with contextlib.closing(db()) as c:
  assert c.execute("select count(*) from task where status not in ('succeeded','failed','cancelled','canceled')").fetchone()[0]==0,'Active task: wait for completion'
  assert c.execute("select count(*) from cube_admission where state='pending'").fetchone()[0]==0,'Provider operation in progress'
  if '--check' in sys.argv:
   print(json.dumps({'preflight':'pass','current_image':BASE,'candidate_image':candidate,'bindings':len(bindings())}));sys.exit(0)
  assert not (ROOT/'before-preview.PRIVATE.sqlite').exists(),'Release already attempted; inspect receipt/backup before retry'
  with sqlite3.connect(ROOT/'before-preview.PRIVATE.sqlite') as backup:c.backup(backup)
 before_bindings=bindings();original={str(p):p.read_bytes() for p in [*FILES,STOP]}
 for p in [*FILES,STOP]:put(ROOT/(p.name+'.before'),original[str(p)])
 oldrender=json.loads(compose('config','--format','json'));candidate=(ROOT/'image.id').read_text().strip()
 assert candidate.startswith('sha256:')
 try:
  for p in FILES:
   value=json.loads(original[str(p)]);svc=value['services']['sandboxd'];svc['image']=candidate
   put(p,json.dumps(value).encode())
  newrender=json.loads(compose('config','--format','json'));expected=json.loads(json.dumps(oldrender));expected['services']['sandboxd']['image']=candidate
  assert newrender==expected,'Unexpected Compose changes'
  compose('up','-d','--no-deps','--no-build','--pull','never','sandboxd')
  compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy')
  ready();after=inspect();assert after['Image']==candidate and after['State']['Running']
  stop=json.loads(original[str(STOP)]);assert stop['controller_id']==before['Id'];stop['controller_id']=after['Id'];put(STOP,json.dumps(stop).encode())
  assert bindings()==before_bindings,'Runtime bindings changed during rollout'
  result={'deployed':True,'controller':after['Id'],'image':candidate,'previous_image':BASE,'bindings_preserved':len(before_bindings)}
  put(ROOT/'deployed.json',json.dumps(result).encode());print(json.dumps(result))
 except BaseException:
  for p in FILES:put(p,original[str(p)])
  compose('up','-d','--no-deps','--no-build','--pull','never','sandboxd')
  compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy');ready()
  stop=json.loads(original[str(STOP)]);stop['controller_id']=inspect()['Id'];put(STOP,json.dumps(stop).encode())
  print('Controller rolled back to previous image/config')
  raise
