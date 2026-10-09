"""Bounded byte-preserving compaction of eight published VPS pause-memory files."""
import importlib.util,json,os,pathlib,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
preferred='01M2AR44CA9AW2FEFA44KVEAGQ'
with b.locked():
 assert json.loads((root/'memory-dedupe-live-canary-01/wakes/complete.json').read_text())['passed']
 out=root/'memory-dedupe-batch-01';out.mkdir(mode=0o700)
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row
  scope=[dict(r) for r in db.execute("select b.sandbox_id,b.runtime_id,b.template_id from runtime_binding b join sandbox s on s.id=b.sandbox_id join cube_admission a on a.runtime_id=b.runtime_id where a.worker_id='vps' and s.status='stopped' and a.state='released' and a.charged=0 order by b.sandbox_id")]
 env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);policy=json.loads(env['SANDBOXD_CUBE_ADMISSION'])
 scope.sort(key=lambda r:(r['sandbox_id']!=preferred,r['sandbox_id']));candidates=[]
 for row in scope:
  if policy['templates'][row['template_id']]['memory_mb']!=768:continue
  info=json.load(urllib.request.urlopen(urllib.request.Request('http://10.254.240.1:18089/cube/sandbox/info?sandbox_id='+row['runtime_id']+'&instance_type=cubebox',headers={'X-Caller':'baarcha-controller'}),timeout=20))['data'][0]
  assert info['sandbox_id']==row['runtime_id'] and info['host_id']=='10.0.2.15'
  if info['status']!=5:continue
  annotations=info['annotations'];annotations=json.loads(annotations) if isinstance(annotations,str) else annotations
  candidates.append({**row,'snapshot_id':annotations['cube.master.pause.snapshot.id'],'memory_bytes':768*1024**2})
  if len(candidates)==8:break
 assert len(candidates)==8 and candidates[0]['sandbox_id']==preferred
 b.atomic(out/'scope.json',b.encoded(candidates))
 code='SCOPE='+repr(candidates)+'\n'+'''import hashlib,json,os,pathlib,re,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/data/cubelet/storage/xfs')
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
out=P('/data/baarcha-memory-dedupe/batch-01');out.mkdir(mode=0o700,parents=True)
files=[]
for row in SCOPE:
 sid=row['snapshot_id'];assert re.fullmatch('snap-[0-9a-f]{24}',sid)
 catalog=json.loads((root/'pause-snapshots'/sid/'metadata/catalog.json').read_text())
 assert catalog['snapshot_id']==sid and catalog['kind']=='pause_snapshot' and catalog['backend']=='xfs' and catalog['memory_kind']=='snapshot'
 name=catalog['memory_vol'];assert name=='tpl-'+sid+'-memory'
 paths=list((root/'objects/volumes').glob('*/'+name));assert len(paths)==1;p=paths[0]
 assert not p.is_symlink() and p.resolve().is_relative_to(root/'objects/volumes') and p.stat().st_uid==0 and p.stat().st_size==row['memory_bytes']
 files.append(p)
def sha(p):
 with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
before=[{**row,'path':str(p),'inode':p.stat().st_ino,'bytes':p.stat().st_size,'sha256':sha(p)} for row,p in zip(SCOPE,files)]
(out/'before.json').write_text(json.dumps(before));fs=os.statvfs('/data');free_before=fs.f_bavail*fs.f_frsize;started=time.monotonic()
# These are published, independent flat memory files. Restore only maps them
# MAP_PRIVATE; every later capture writes a new snapshot ID. Removing zero
# allocation therefore preserves the complete image, including unchanged pages.
for p in files:
 subprocess.run(['nice','-n','15','ionice','-c','3','fallocate','--dig-holes','--',str(p)],check=True,timeout=120)
with (out/'duperemove.log').open('wb') as log:
 subprocess.run(['nice','-n','15','ionice','-c','3','duperemove','-d','-b','64K','--skip-zeroes','--io-threads=1','--cpu-threads=1','--dedupe-options=partial,same,nofiemap','--hashfile='+str(out/'hashes.db'),*[str(p) for p in files]],check=True,stdout=log,stderr=subprocess.STDOUT,timeout=900)
for p,saved in zip(files,before):
 assert sha(p)==saved['sha256'] and p.stat().st_ino==saved['inode'] and p.stat().st_size==saved['bytes']
 with p.open('rb') as f:os.fsync(f.fileno())
fs=os.statvfs('/data');result={'passed':True,'files':before,'count':len(files),'contents_identical':True,'inodes_preserved':True,'block_size':65536,'filesystem_free_delta_bytes':fs.f_bavail*fs.f_frsize-free_before,'data_used_bytes':(fs.f_blocks-fs.f_bfree)*fs.f_frsize,'data_total_bytes':fs.f_blocks*fs.f_frsize,'elapsed_seconds':time.monotonic()-started,'at':time.time()};(out/'complete.json').write_text(json.dumps(result));print(json.dumps(result))
'''
 b.atomic(out/'worker.py',code.encode())
 result=json.loads(subprocess.check_output(ssh+['python3','-'],input=code.encode(),timeout=1200));b.atomic(out/'complete.json',b.encoded(result));print(json.dumps({k:v for k,v in result.items() if k!='files'}))
