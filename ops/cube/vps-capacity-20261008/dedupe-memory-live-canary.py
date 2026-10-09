"""Deduplicate two immutable VPS memory files, proving identical contents."""
import importlib.util,json,os,pathlib,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
sid='01M2AR44CA9AW2FEFA44KVEAGQ';rid='e3a26535a2874c77a799e45f40158cd1'
with b.locked():
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0
  assert db.execute('select s.status,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,)).fetchone()==('stopped',rid,'released',0)
 info=json.load(urllib.request.urlopen(urllib.request.Request('http://10.254.240.1:18089/cube/sandbox/info?sandbox_id='+rid+'&instance_type=cubebox',headers={'X-Caller':'baarcha-controller'}),timeout=20))['data'][0]
 assert info['status']==5 and info['host_id']=='10.0.2.15'
 annotations=info['annotations'];annotations=json.loads(annotations) if isinstance(annotations,str) else annotations
 snap=annotations['cube.master.pause.snapshot.id'];assert snap=='snap-3b6736231a0d40dc9b83cb7f'
 out=root/'memory-dedupe-live-canary-01';out.mkdir(mode=0o700)
 code='SNAP='+repr(snap)+'\n'+'''import hashlib,json,os,pathlib,re,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/data/cubelet/storage/xfs')
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
prior=json.loads(P('/data/baarcha-memory-dedupe-canary-20261009/partial-complete.json').read_text());assert prior['passed'] and prior['partial_blocks'] and prior['contents_identical']
out=P('/data/baarcha-memory-dedupe-live-canary-01');out.mkdir(mode=0o700)
files=[]
for sid in [SNAP,'snap-c2f32aa68d9c4cf48dc08579']:
 assert re.fullmatch('snap-[0-9a-f]{24}',sid)
 catalog=json.loads((root/'pause-snapshots'/sid/'metadata/catalog.json').read_text())
 assert catalog['snapshot_id']==sid and catalog['kind']=='pause_snapshot' and catalog['backend']=='xfs' and catalog['memory_kind']=='snapshot'
 name=catalog['memory_vol'];assert name=='tpl-'+sid+'-memory'
 paths=list((root/'objects/volumes').glob('*/'+name));assert len(paths)==1;p=paths[0]
 assert not p.is_symlink() and p.resolve().is_relative_to(root/'objects/volumes') and p.stat().st_uid==0 and p.stat().st_size==768*1024**2
 files.append(p)
def sha(p):
 with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
before=[{'path':str(p),'inode':p.stat().st_ino,'bytes':p.stat().st_size,'sha256':sha(p)} for p in files]
(out/'before.json').write_text(json.dumps(before));fs=os.statvfs('/data');free_before=fs.f_bavail*fs.f_frsize;started=time.monotonic()
with (out/'duperemove.log').open('wb') as log:
 subprocess.run(['nice','-n','15','ionice','-c','3','duperemove','-d','-b','64K','--io-threads=1','--cpu-threads=1','--dedupe-options=partial,same,nofiemap','--hashfile='+str(out/'hashes.db'),*[str(p) for p in files]],check=True,stdout=log,stderr=subprocess.STDOUT,timeout=600)
for p,saved in zip(files,before):
 assert sha(p)==saved['sha256'] and p.stat().st_ino==saved['inode'] and p.stat().st_size==saved['bytes']
 with p.open('rb') as f:os.fsync(f.fileno())
fs=os.statvfs('/data');result={'passed':True,'snapshot_id':SNAP,'files':before,'contents_identical':True,'inodes_preserved':True,'block_size':65536,'filesystem_free_delta_bytes':fs.f_bavail*fs.f_frsize-free_before,'elapsed_seconds':time.monotonic()-started,'at':time.time()};(out/'complete.json').write_text(json.dumps(result));print(json.dumps(result))
'''
 b.atomic(out/'worker.py',code.encode())
 result=json.loads(subprocess.check_output(ssh+['python3','-'],input=code.encode(),timeout=650));b.atomic(out/'deduped.json',b.encoded(result));print(json.dumps(result))
