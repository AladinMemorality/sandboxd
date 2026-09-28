#!/usr/bin/env python3
"""Install reviewed API/controller fixes, reconcile only drained known mutations.

Private release inputs pin the existing container, images, config and bindings.
No project files move and no customer runtime is replaced. Run on VPS as root.
"""
import contextlib,hashlib,http.client,importlib.util,json,os,sqlite3,subprocess,time
from pathlib import Path
ROOT=Path('/opt/baarcha-bench/cube-fleet-20260927/capacity-ready')
s=importlib.util.spec_from_file_location('m',ROOT/'relocation-canary.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
JOB=ROOT/'controller-01';p=json.loads((JOB/'release.PRIVATE.json').read_text());b=m.r.b
WORKER=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-p','20222','root@127.0.0.1']
def run(args,**kwargs):return subprocess.check_output(args,timeout=220,**kwargs)
def event(phase,**data):
 value=dict(phase=phase,at=time.time(),**data);m.c.save(JOB/'phase.json',value);print(json.dumps(value),flush=True)
def inspect():return json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
def routing(value):
 c=http.client.HTTPConnection('127.0.0.1',2019,timeout=15)
 try:
  c.request('POST','/load',json.dumps(value),{'Content-Type':'application/json'});r=c.getresponse();r.read();assert r.status==200
 finally:c.close()
 assert json.loads(b.http('/config/',2019))==value

def main():
 with b.locked():
  cp=inspect();assert cp['Id']==p['old_id'] and cp['Image']==p['old_image'] and cp['State']['Running']
  assert not m.c.rows("SELECT task_id FROM task WHERE status='running'")
  assert not m.c.rows("SELECT admission_key FROM cube_admission WHERE state='pending' AND operation<>'connect'")
  assert m.c.rows('SELECT sandbox_id,runtime_id FROM runtime_binding ORDER BY sandbox_id')==p['bindings']
  for path,digest in p['files'].items():assert hashlib.sha256(Path(path).read_bytes()).hexdigest()==digest
  bridge=b.Host({});assert json.loads(bridge.compose('config','--format','json'))==p['before']
  assert json.loads(b.http('/config/',2019))==p['routes']['online']
  candidate=ROOT/'native-api-01/CubeAPI/target/release/cube-api'
  assert hashlib.sha256(candidate.read_bytes()).hexdigest()==p['native_sha']
  timers={name:run(['systemctl','show',name,'-p','ActiveState','--value']).decode().strip() for name in (*b.TIMERS,'baarcha-motion-access.timer')}
  assert all(v in ('active','inactive') for v in timers.values())
  m.c.save(JOB/'timer-before.json',timers)
  for path in p['files']:m.c.save(JOB/(Path(path).name+'.before.PRIVATE.json'),json.loads(Path(path).read_text()))
  routing(p['routes']['offline']);event('traffic-fenced')
  stopped=False;installed=False
  try:
   run(['systemctl','stop',*timers])
   assert not m.c.rows("SELECT task_id FROM task WHERE status='running'")
   run(['docker','update','--restart=no',p['old_id']])
   run(['docker','stop','--time=-1',p['old_id']]);stopped=True
   cp=inspect();assert not cp['State']['Running'] and cp['State']['ExitCode']==0
   event('controller-stopped-provider-drain')
   # Provider mutations detach from cancelled HTTP callers for up to175s.
   # Keep the writer stopped beyond that bound before authoritative recovery.
   for i in range(7):time.sleep(27);event('provider-drain',elapsed=(i+1)*27)
   with contextlib.closing(sqlite3.connect('/var/lib/sandboxd/state/sandboxd.db')) as db:
    with sqlite3.connect(JOB/'before-reconcile.PRIVATE.sqlite') as backup:db.backup(backup)
   os.chmod(JOB/'before-reconcile.PRIVATE.sqlite',0o600)
   pending=m.c.rows("SELECT admission_key,runtime_id,worker_id,operation,state FROM cube_admission WHERE state='pending'")
   assert all(v['operation']=='connect' and v['runtime_id'] for v in pending)
   m.c.save(JOB/'pending-before.PRIVATE.json',pending)
   stage='/root/cube-production/fleet-capacity-api-20260928'
   run(WORKER+['mkdir -p '+stage])
   run(WORKER+['cat > '+stage+'/cube-api.new'],input=candidate.read_bytes())
   command="set -eu; test \"$(sha256sum /usr/local/services/cubetoolbox/CubeAPI/bin/cube-api | cut -d' ' -f1)\" = e3cfe133e96b8a48e2d8b4c3a1723438a16b6020701426bd09aa9592ea283a08; test \"$(sha256sum "+stage+"/cube-api.new | cut -d' ' -f1)\" = "+p['native_sha']+"; cp -p /usr/local/services/cubetoolbox/CubeAPI/bin/cube-api "+stage+"/cube-api.previous; systemctl stop cube-sandbox-cube-api.service; install -m755 "+stage+"/cube-api.new /usr/local/services/cubetoolbox/CubeAPI/bin/cube-api; systemctl start cube-sandbox-cube-api.service; systemctl is-active cube-sandbox-cube-api.service"
   assert run(WORKER+[command]).strip()==b'active';event('native-api-installed',sha256=p['native_sha'])
   env=dict(os.environ,**p['environment']);env['SANDBOXD_CUBE_API_URL']='http://127.0.0.1:20300'
   fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);fleet['master_url']='http://10.254.240.1:18089';env['SANDBOXD_CUBE_FLEET']=json.dumps(fleet)
   # Existing ConfigureFleet/ReconcileAdmission validates native identity,
   # resource profile, template and worker before an exact admission-token CAS.
   for row in pending:
    result=subprocess.run([str(JOB/'cube-migrate'),'--database','/var/lib/sandboxd/state/sandboxd.db','--migrations',str(JOB/'control-plane/migrations'),'--provider-requests-drained','--admission-key',row['admission_key'],'admission-reconcile'],env=env,capture_output=True,timeout=90)
    if result.returncode:
     (JOB/'reconcile-error.PRIVATE.log').write_bytes(result.stderr);raise RuntimeError('offline reconciliation failed; private diagnostic retained')
   assert not m.c.rows("SELECT admission_key FROM cube_admission WHERE state='pending'")
   event('admission-reconciled',count=len(pending))
   for path in (b.COMPOSE,b.ACTIVE):
    assert hashlib.sha256(path.read_bytes()).hexdigest()==p['files'][str(path)]
    value=json.loads(path.read_text());value['services']['sandboxd']['image']=p['new_image'];b.atomic(path,b.encoded(value))
   assert json.loads(bridge.compose('config','--format','json'))==p['after']
   bridge.activate();bridge.compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-master','cube-management-b200-proxy');installed=True
   new=inspect();assert new['Image']==p['new_image']
   assert new['Config']['Env']==cp['Config']['Env'] and new['Config']['Entrypoint']==cp['Config']['Entrypoint']
   stop=json.loads(b.STOP.read_text());assert stop['controller_id']==p['old_id'];stop['controller_id']=new['Id'];b.atomic(b.STOP,b.encoded(stop))
   for _ in range(60):
    try:
     if m.c.request('127.0.0.1',9090,'/readyz',timeout=2)[0]==200:break
    except OSError:pass
    time.sleep(1)
   else:raise RuntimeError('controller not ready')
   assert m.c.rows('SELECT sandbox_id,runtime_id FROM runtime_binding ORDER BY sandbox_id')==p['bindings']
   event('controller-ready',controller=new['Id'],image=new['Image'])
  finally:
   # Restore public routing only to a healthy controller. Preserve the offline
   # fence and journal on failure for explicit recovery; never rewrite data.
   healthy=False
   try:healthy=m.c.request('127.0.0.1',9090,'/readyz',timeout=2)[0]==200
   except OSError:pass
   if healthy:
    for name,state in timers.items():
     if state=='active':run(['systemctl','start',name])
    routing(p['routes']['online']);event('online-restored',installed=installed)
if __name__=='__main__':main()
