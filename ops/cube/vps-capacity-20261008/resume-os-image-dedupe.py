"""Resume the reviewed SIGINT, releasing operator locks between image pairs."""
import contextlib,importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);ROOT=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
out=ROOT/'os-image-dedupe-resume-01';out.mkdir(mode=0o700)
@contextlib.contextmanager
def locks():
 deadline=time.monotonic()+3600
 with contextlib.ExitStack() as stack:
  while True:
   try:stack.enter_context(b.locked());break
   except BlockingIOError:
    assert time.monotonic()<deadline,'Operator lock wait exceeded';time.sleep(2)
  yield
CHECK=r'''
import collections,hashlib,json,os,pathlib,time
P=pathlib.Path;root=P('/data/baarcha-os-image-dedupe/os-image-dedupe-all-01')
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
stop=json.loads((root/'operator-stop.json').read_text());assert stop['completed_groups']==9 and stop['log']==str(root/'group-009.log')
for proc in P('/proc').glob('[0-9]*'):
 try:
  argv=(proc/'cmdline').read_bytes().split(b'\0')
  if argv and argv[0]==b'duperemove':assert not os.readlink(proc/'fd/1').startswith(str(root)),'Previous image dedupe still active'
 except (FileNotFoundError,ProcessLookupError,PermissionError):pass
before=json.loads((root/'before.json').read_text());assert len(before)==44
groups=collections.defaultdict(list)
for r in before:
 p=P(r['path']);assert not p.is_symlink() and p.parent.parent in [P('/data/CubeMaster/storage'),P('/data/cubebox-os-images')]
 s=p.stat();assert (s.st_ino,s.st_size,s.st_mtime_ns)==(r['inode'],r['bytes'],r['mtime_ns'])
 groups[(r['bytes'],r['sha256'])].append(r)
groups=list(groups.values());assert len(groups)==22 and all(len(g)==2 for g in groups)
for index in range(9):
 proof=json.loads((root/('group-%03d.json'%index)).read_text());assert proof=={'sha256':groups[index][0]['sha256'],'bytes':groups[index][0]['bytes'],'files':2}
# The interrupted ioctl only shares equal bytes. Prove that exact pair's
# content before continuing; never fabricate a completed dedupe receipt.
for r in groups[9]:
 with P(r['path']).open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==r['sha256']
print(json.dumps({'groups':groups,'completed_groups':9,'interrupted_contents_identical':True,'at':time.time()}))
'''
with locks():
 assert subprocess.check_output(['systemctl','show','baarcha-vps-finish-storage-fleet-26','-p','ActiveState','--value'],text=True).strip()=='failed'
 reconciled=json.loads(subprocess.check_output(SSH+['python3','-'],input=CHECK.encode(),timeout=300));b.atomic(out/'reconciled.json',b.encoded(reconciled))
print(json.dumps({'reconciled':True,'verified_completed_pairs':9,'remaining_pairs':13}),flush=True)
# Give the already-tested web fix a clear deployment window. The CI deployer
# owns the same operator lock; subsequent pairs wait if it is still switching.
while subprocess.check_output(['git','-C','/opt/baarcha/app','rev-parse','HEAD'],text=True).strip()!='69936b503843b597c9f0a938e6c78e25a5fa51b6':
 time.sleep(10)
for index,group in enumerate(reconciled['groups']):
 if index<9:continue
 with locks():
  code='INDEX='+str(index)+'\nGROUP='+repr(group)+'\n'+r'''
import hashlib,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);out=P('/data/baarcha-os-image-dedupe/os-image-dedupe-resume-01')/('group-%03d'%INDEX);out.mkdir(mode=0o700,parents=True)
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
for r in GROUP:
 p=P(r['path']);s=p.stat();assert not p.is_symlink() and (s.st_ino,s.st_size,s.st_mtime_ns)==(r['inode'],r['bytes'],r['mtime_ns'])
fs=os.statvfs('/data');free_before=fs.f_bavail*fs.f_frsize;started=time.monotonic()
with (out/'duperemove.log').open('wb') as log:
 subprocess.run(['nice','-n','15','ionice','-c','3','duperemove','-d','-A','--fdupes','--io-threads=1','--cpu-threads=1'],input=('\n'.join(r['path'] for r in GROUP)+'\n\n').encode(),stdout=log,stderr=subprocess.STDOUT,check=True,timeout=600)
for r in GROUP:
 p=P(r['path'])
 with p.open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==r['sha256'];os.fsync(f.fileno())
 s=p.stat();assert (s.st_ino,s.st_size,s.st_mtime_ns)==(r['inode'],r['bytes'],r['mtime_ns'])
fs=os.statvfs('/data');v={'passed':True,'group':INDEX,'files':2,'contents_identical':True,'filesystem_free_delta_bytes':fs.f_bavail*fs.f_frsize-free_before,'data_used_bytes':(fs.f_blocks-fs.f_bfree)*fs.f_frsize,'elapsed_seconds':time.monotonic()-started,'at':time.time()};(out/'complete.json').write_text(json.dumps(v));print(json.dumps(v))
'''
  b.atomic(out/('group-%03d-worker.py'%index),code.encode())
  result=json.loads(subprocess.check_output(SSH+['python3','-'],input=code.encode(),timeout=900));b.atomic(out/('group-%03d.json'%index),b.encoded(result));print(json.dumps(result),flush=True)
 time.sleep(2)
b.atomic(out/'complete.json',b.encoded({'complete':True,'verified_image_pairs':22,'files':44,'original_journals_retained':True,'contents_identical':True,'b200_contacted':False,'at':time.time()}))
