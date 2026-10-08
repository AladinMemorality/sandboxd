"""Record VPS-only inactive orphan inventory for a nondestructive worker cycle.

No orphan is deleted or resumed. Future activity in any retained record makes
the native lifecycle audit fail. Canonically bound records cannot be excluded.
"""
import fcntl,json,os,shutil,sqlite3,urllib.request
from pathlib import Path
os.umask(0o077)
root=Path('/opt/baarcha/operations/vps-50-profiles-20261008')
locks=[]
for path in ('/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock'):
    f=open(path,'a');fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB);locks.append(f)
assert 'FAIL' not in (root/'source/scoped-native-tests.log').read_text()
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
    all_bound={r[0] for r in db.execute('SELECT runtime_id FROM runtime_binding')}
    local={r[0] for r in db.execute("SELECT b.runtime_id FROM runtime_binding b JOIN cube_admission a USING(runtime_id) WHERE a.worker_id='vps'")}
assert len(local)==7
value=json.load(urllib.request.urlopen('http://10.254.240.1:18089/cube/sandbox/inventory?host_id=10.0.2.15',timeout=30))
assert value['ret']['ret_code']==200 and isinstance(value['data'],list)
seen=set();retained=[]
for row in value['data']:
    ident=row['sandbox_id'];assert ident not in seen and row['host_id']=='10.0.2.15';seen.add(ident)
    if ident in local:continue
    assert ident not in all_bound and row['status'] in (2,5)
    retained.append({'runtime_id':ident,'status':row['status']})
assert local<=seen
path=Path('/etc/baarcha-cube/worker-stop.json');before=path.read_bytes();stop=json.loads(before)
assert stop['worker_id']=='vps' and not stop.get('retained_inactive')
with (root/'retained-stop-before.PRIVATE.json').open('xb') as f:f.write(before)
(root/'retained-inactive.json').write_text(json.dumps(retained,indent=2))
stop['retained_inactive']=sorted(r['runtime_id'] for r in retained)
pending=path.with_suffix('.retained-pending');pending.write_text(json.dumps(stop));pending.chmod(0o600);os.replace(pending,path)
for name in ('start','stop'):
    target=Path('/usr/local/libexec/baarcha-cube-worker-'+name)
    pending=target.with_suffix('.scoped-pending');shutil.copyfile(root/('source/worker-'+name+'-candidate'),pending);pending.chmod(0o755);os.replace(pending,target)
print(json.dumps({'canonical_vps':len(local),'retained_inactive':len(retained),'runtime_mutations':0}))
