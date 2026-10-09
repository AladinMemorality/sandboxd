"""Read-only database check of the app whose saved statistics inode was stale."""
import importlib.util,json,os,pathlib,shlex,sqlite3,subprocess,time
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M3D1Q0E1KM1FEM244XVHEC65';rid='095a0076b90b4b009ab995837909e276'
def module(n,p):
 spec=importlib.util.spec_from_file_location(n,p);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
b=module('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');copy=module('copy','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py')
def rows(q,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:return db.execute(q,args).fetchall()
with b.locked():
 out=root/'captured-postgres-check-80';out.mkdir(mode=0o700)
 assert rows('select s.status,b.runtime_id from sandbox s join runtime_binding b on b.sandbox_id=s.id where s.id=?',(sid,))==[('stopped',rid)]
 history=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,));assert not any(t[1] in ('running','queued') for t in history)
 with account_maintenance([sid],out):
  try:
   code,_=copy.api('POST','/v1/sandboxes/'+sid+'/start');assert code==200
   guest=r'''import json,os,pathlib,tty,glob
try:
 tty.setraw(0);print('RUNTIME_READY',flush=True)
 p=pathlib.Path('/home/sandbox/.baarcha-postgres/data');lines=(p/'postmaster.pid').read_text().splitlines();processes=[]
 for path in pathlib.Path('/proc').glob('[0-9]*/comm'):
  try:
   if 'postgres' in path.read_text():processes.append({'pid':path.parent.name,'exe':os.readlink(path.parent/'exe')})
  except OSError:pass
 bins=[]
 for pattern in ['/usr/bin/psql','/usr/local/bin/psql','/opt/*/bin/psql','/opt/*/*/bin/psql','/home/sandbox/.local/*/bin/psql','/home/sandbox/.local/*/*/bin/psql']:
  bins.extend(glob.glob(pattern))
 log=pathlib.Path('/home/sandbox/.runtimed/postgres.log')
 print('RUNTIME_RECEIPT='+json.dumps({'database_query_passed':False,'postmaster_fields':lines[:5],'postgres_processes':processes,'client_paths':bins,'postgres_log':log.read_text(errors='replace')[-8000:] if log.exists() else None}),flush=True)
except BaseException as e:print('RUNTIME_RECEIPT='+json.dumps({'error':type(e).__name__,'message':str(e)}),flush=True)
'''
   inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-export-recovery-2c7e700');import worker;print(json.dumps(worker.execute("+repr(rid)+","+repr(guest)+",'{}',b'')))"
   ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
   result=subprocess.run(ssh+['python3 -c '+shlex.quote(inner)],capture_output=True,timeout=150);b.atomic(out/'check.PRIVATE.log',result.stdout+result.stderr);assert result.returncode==0
   proof=json.loads(result.stdout);b.atomic(out/'result.json',b.encoded(proof));assert proof.get('database_query_passed'), 'Review retained database diagnostics'
  finally:
   assert rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==history
   code,_=copy.api('POST','/v1/sandboxes/'+sid+'/stop');assert code==200
  assert rows('select status from sandbox where id=?',(sid,))==[('stopped',)]
 b.atomic(out/'complete.json',b.encoded({'passed':True,'original_state_restored':True,'database':proof,'b200_contacted':False,'at':time.time()}));print(json.dumps(proof),flush=True)
