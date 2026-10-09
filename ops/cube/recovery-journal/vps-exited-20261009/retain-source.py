"""Enroll the completed recovery's unbound, stopped original in retained inventory.

The existing worker audit continues to reject this runtime if it becomes active.
No original provider object, disk or snapshot is mutated.
"""
import contextlib,importlib.util,json,pathlib,sqlite3,subprocess,urllib.request
P=pathlib.Path;r=P('/opt/baarcha/operations/vps-50-profiles-20261008/exited-recovery-01');old='38a1bd34cfa54a9aa47b4b121d035e46';sid='01M24GEQ88YGHHH3WPC9WD9DY0'
spec=importlib.util.spec_from_file_location('b','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
 receipt=json.loads((r/'complete.json').read_text());assert receipt['restored'] and receipt['sandbox_id']==sid
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
  assert db.execute('select phase,old_runtime_id,new_runtime_id from cube_recovery where recovery_id=?',('vps-exited-20261009',)).fetchone()==('complete',old,receipt['runtime_id'])
  assert db.execute('select count(*) from runtime_binding where runtime_id=?',(old,)).fetchone()[0]==0
  assert db.execute('select sandbox_id,recovery_id from cube_runtime_quarantine where runtime_id=?',(old,)).fetchone()==(sid,'vps-exited-20261009')
 cfg=json.loads((r/'operator-config.PRIVATE.json').read_text());req=urllib.request.Request(cfg['Provider']['APIURL']+'/sandboxes/'+old,headers={'X-API-Key':cfg['Provider']['APIKey']});v=json.load(urllib.request.urlopen(req,timeout=15));assert v['sandboxID']==old and v['state']=='stopped'
 path=P('/etc/baarcha-cube/worker-stop.json');raw=b.trusted(path);value=json.loads(raw);assert value['worker_id']=='vps' and old not in value['retained_inactive']
 b.atomic(r/'retained-stop-before.PRIVATE.json',raw);value['retained_inactive']=sorted([*value['retained_inactive'],old]);b.atomic(path,b.encoded(value))
 result=json.loads(subprocess.check_output(['/usr/local/libexec/baarcha-cube-worker-start','--observe'],timeout=200));assert result['consistent']
 b.atomic(r/'retained-source.json',b.encoded({'runtime_id':old,'unbound':True,'quarantined':True,'native_state':'stopped','observation':result,'provider_mutations':0}));print(json.dumps(result))
