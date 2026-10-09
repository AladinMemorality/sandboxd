"""Share identical VPS OS-image blocks without replacing files or changing bytes."""
import argparse,importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);ROOT=P('/opt/baarcha/operations/vps-50-profiles-20261008')
parser=argparse.ArgumentParser();parser.add_argument('--all',action='store_true');args=parser.parse_args()
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
with b.locked():
 if args.all:assert json.loads((ROOT/'os-image-dedupe-canary-01/complete.json').read_text())['contents_identical']
 name='os-image-dedupe-all-01' if args.all else 'os-image-dedupe-canary-01';out=ROOT/name;out.mkdir(mode=0o700)
 code='ALL='+repr(args.all)+'\nNAME='+repr(name)+'\n'+r'''
import collections,hashlib,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077)
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
out=P('/data/baarcha-os-image-dedupe')/NAME;out.mkdir(mode=0o700,parents=True)
roots=[P('/data/CubeMaster/storage'),P('/data/cubebox-os-images')]
canary='rfs-4f45a7f572bb615720f5dcc0-3f7d58fa'
files=[]
for root in roots:
 for p in sorted(root.glob('rfs-*/*.ext4')):
  if not ALL and p.parent.name!=canary:continue
  assert p.name==p.parent.name+'.ext4' and not p.is_symlink() and p.resolve().is_relative_to(root)
  s=p.stat();assert s.st_uid==0 and 3*1024**3<=s.st_size<=8*1024**3
  files.append(p)
assert len(files)>=2
# Detect a concurrently rewritten image before submitting it. Kernel-side
# comparison still rejects changed data; whole-image hashes prove the result.
def fingerprint(p):
 s=p.stat()
 with p.open('rb') as f:sha=hashlib.file_digest(f,'sha256').hexdigest()
 a=p.stat();assert (s.st_ino,s.st_size,s.st_mtime_ns)==(a.st_ino,a.st_size,a.st_mtime_ns)
 return {'path':str(p),'inode':s.st_ino,'bytes':s.st_size,'mtime_ns':s.st_mtime_ns,'sha256':sha}
def free():
 s=os.statvfs('/data');return s.f_bavail*s.f_frsize
started=time.monotonic();free_before=free();before=[fingerprint(p) for p in files]
(out/'before.json').write_text(json.dumps(before));groups=collections.defaultdict(list)
for row in before:groups[(row['bytes'],row['sha256'])].append(row['path'])
completed=[]
for index,((size,sha),paths) in enumerate(groups.items()):
 if len(paths)<2:continue
 with (out/('group-%03d.log'%index)).open('wb') as log:
  subprocess.run(['nice','-n','15','ionice','-c','3','duperemove','-d','-A','--fdupes','--io-threads=1','--cpu-threads=1'],input=('\n'.join(paths)+'\n\n').encode(),stdout=log,stderr=subprocess.STDOUT,check=True,timeout=300)
 for path in paths:
  expected=next(r for r in before if r['path']==path);assert fingerprint(P(path))==expected
 completed.append({'sha256':sha,'bytes':size,'files':len(paths)})
 (out/('group-%03d.json'%index)).write_text(json.dumps(completed[-1]))
assert completed
fs=os.statvfs('/data');result={'complete':True,'contents_identical':True,'paths_and_inodes_preserved':True,'checked_files':len(files),'groups':completed,'filesystem_free_delta_bytes':free()-free_before,'data_used_bytes':(fs.f_blocks-fs.f_bfree)*fs.f_frsize,'data_total_bytes':fs.f_blocks*fs.f_frsize,'elapsed_seconds':time.monotonic()-started,'b200_contacted':False,'at':time.time()}
(out/'complete.json').write_text(json.dumps(result));print(json.dumps(result))
'''
 b.atomic(out/'worker.py',code.encode())
 result=json.loads(subprocess.check_output(SSH+['python3','-'],input=code.encode(),timeout=1800));b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result))
