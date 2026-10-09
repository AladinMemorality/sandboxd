"""Bind the reviewed owner-home scope of the new VPS app to its verified backup."""
import hashlib,importlib.util,json,os,pathlib,sqlite3,tarfile
P=pathlib.Path;os.umask(0o077);sid='01M4EP7FC5TTW0RZS8SKTGX9RV'
root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
backup=P('/var/backups/baarcha-vps-source/20261008T222308Z/sandboxes')/sid
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
 receipt=json.loads((backup/'receipt.json').read_text());verified=json.loads((backup/'verified.json').read_text())
 digest=hashlib.sha256()
 with (backup/'home.tar.gz').open('rb') as f:
  for chunk in iter(lambda:f.read(1024**2),b''):digest.update(chunk)
 assert digest.hexdigest()==receipt['sha256']==verified['sha256']
 assert receipt['sandbox_id']==sid and receipt['worker']=='vps'
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()==(receipt['runtime_id'],)
  assert not db.execute("select task_id from task where sandbox_id=? and status in ('running','queued')",(sid,)).fetchall()
 with tarfile.open(backup/'home.tar.gz') as t:names=[m.name.removeprefix('./') for m in t]
 top=sorted(set(n.split('/')[0] for n in names)-{'.',''})
 assert top==['.bash_logout','.bashrc','.cache','.claude','.claude.json','.profile','.runtimed','workspace']
 assert set(n.split('/')[1] for n in names if n.startswith('workspace/') and len(n.split('/'))>1)=={'app'}
 entries=[{'path':'.runtimed','disposition':'separate'},{'path':'workspace/app','disposition':'separate'}]
 for name in top:
  if name in ('.runtimed','workspace'):continue
  item={'path':name,'disposition':'preserve'}
  if name in ('.claude','.claude.json'):item.update(disposition='retained',reason='Provider authentication and local session state remain in the retained private source and verified VPS backup; canonical tasks use the separate history channel')
  entries.append(item)
 out=root/'reprofile-owner-manifests';out.mkdir(mode=0o700,exist_ok=True)
 target=out/(sid+'.json');assert not target.exists()
 b.atomic(target,b.encoded({'sandbox_id':sid,'runtime_id':receipt['runtime_id'],'backup_verified':True,'backup_sha256':digest.hexdigest(),'same_owner_only':True,'home_manifest':{'version':2,'entries':entries}}))
 print(json.dumps({'reviewed':sid,'backup_verified':True,'source_binding_unchanged':True}))
