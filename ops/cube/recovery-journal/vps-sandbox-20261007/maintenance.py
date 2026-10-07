"""Exact Brandish VPS incident; retained sources and controller state are never deleted."""
import json,os,pathlib,shutil,sqlite3,subprocess,sys,time
os.umask(0o077)
r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007');name='src-sandboxd-1'
def inspect():
 x=json.loads(subprocess.check_output(['docker','inspect',name]))[0]
 assert x['Id']==json.loads((r/'operator-identity.json').read_text())['controller_id']
 return x
def db():return sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)
x=inspect()
if sys.argv[1]=='enter':
 assert x['State']['Running'] and x['HostConfig']['RestartPolicy']['Name']=='unless-stopped'
 with db() as c:
  assert c.execute("SELECT count(*) FROM task WHERE status IN ('running','queued')").fetchone()[0]==0
  assert c.execute("SELECT count(*) FROM cube_recovery WHERE phase<>'complete'").fetchone()[0]==0
  assert c.execute("SELECT count(*) FROM cube_admission WHERE state='pending' AND operation='create'").fetchone()[0]==0
 assert not (r/'controller-before.db').exists()
 (r/'maintenance-entered.json').write_text(json.dumps({'container_id':x['Id'],'at':time.time()}))
 subprocess.run(['docker','update','--restart=no',name],check=True,capture_output=True)
 subprocess.run(['docker','stop','--time','45',name],check=True,capture_output=True,timeout=100)
 assert not inspect()['State']['Running']
 with db() as c:
  assert c.execute("SELECT count(*) FROM task WHERE status IN ('running','queued')").fetchone()[0]==0
  with sqlite3.connect(r/'controller-before.db') as dest:
   c.backup(dest);assert dest.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
 shutil.copyfile('/var/lib/sandboxd/secrets.key',r/'controller-key.PRIVATE')
 (r/'fence.json').write_text(json.dumps({'OldRuntimeID':'f51584459b354470b04c966ae8429293','OldExecutionStopped':True,'ProviderRequestsDrained':True,'Expires':int(time.time())+1200,'controller_stopped':True,'source_rechecked_under_worker_lock_by_operator':True}))
 print('controller fenced; consistent database/key retained')
elif sys.argv[1]=='exit':
 if not (r/'maintenance-entered.json').exists():print('controller maintenance was not entered');sys.exit(0)
 with db() as c:n=c.execute("SELECT count(*) FROM cube_recovery WHERE phase<>'complete'").fetchone()[0]
 if n:print('controller remains fenced: incomplete recovery journal');sys.exit(1)
 subprocess.run(['docker','start',name],check=True,capture_output=True)
 subprocess.run(['docker','update','--restart=unless-stopped',name],check=True,capture_output=True)
 subprocess.run(['docker','compose','--project-directory','/opt/sandboxd/src','-f','/opt/sandboxd/src/docker-compose.yml','-f','/opt/sandboxd/deploy-state/runtime-compose.json','-f','/opt/sandboxd/deploy-state/active-images.json','up','-d','--no-deps','--force-recreate','cube-management-api','cube-management-master','cube-management-proxy','cube-management-b200-proxy'],check=True,capture_output=True)
 print('controller and management transports restored')
else:raise ValueError('enter or exit required')
