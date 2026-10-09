"""Move verified inactive transfer archives to HDD; preserve their original paths."""
import hashlib,importlib.util,json,os,pathlib,shutil,signal,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'cold-archive-resume-01';old=root/'cold-archive-move';destroot=P('/var/backups/baarcha-cold-runtime/20261009')
names=['migration-archives','fleet-artifact-stage'];sources=[P('/mnt/nvme/baarcha-cube')/n for n in names]
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
def below(value,source):return value==str(source) or value.startswith(str(source)+'/')
def unreferenced(source):
 ids=subprocess.check_output(['docker','ps','-aq'],text=True).split()
 for container in json.loads(subprocess.check_output(['docker','inspect',*ids])):
  for mount in container.get('Mounts',[]):
   path=P(mount['Source']);assert not (path==source or path in source.parents or source in path.parents),'Container mount references archive'
 for proc in P('/proc').glob('[0-9]*'):
  try:
   for kind in ['cwd','root','exe']:
    try:value=os.readlink(proc/kind)
    except OSError:continue
    assert not below(value,source),'Archive is a process working directory, root or executable'
   for fd in (proc/'fd').iterdir():
    try:value=os.readlink(fd)
    except OSError:continue
    assert not below(value,source),'Archive has an open descriptor'
   assert not any(below(line.split(maxsplit=5)[-1],source) for line in (proc/'maps').read_text().splitlines() if len(line.split(maxsplit=5))==6),'Archive is mapped'
  except (FileNotFoundError,ProcessLookupError,PermissionError):pass
 for f in P('/sys/block').glob('loop*/loop/backing_file'):assert not below(f.read_text().strip(),source),'Archive is a loop backing file'
def metadata(source):
 result={}
 for base,ds,fs in os.walk(source):
  for name in ds+fs:
   p=P(base)/name;s=p.lstat();result[str(p.relative_to(source))]=[s.st_mode,s.st_uid,s.st_gid,s.st_size,s.st_mtime_ns,s.st_ctime_ns,s.st_ino]
 return result
def free(path):s=os.statvfs(path);return s.f_bavail*s.f_frsize
def ready():
 with urllib.request.urlopen('http://127.0.0.1:9090/readyz',timeout=5) as r:assert r.read().strip()==b'ready'
def checked_run(args,logpath,timeout):
 ready();deadline=time.monotonic()+timeout
 with logpath.open('xb') as log:
  proc=subprocess.Popen(args,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
  try:
   while proc.poll() is None:
    assert time.monotonic()<deadline,'Archive operation timed out'
    ready();time.sleep(3)
   assert proc.returncode==0,'Archive command failed; private log retained'
  except BaseException:
   if proc.poll() is None:
    os.killpg(proc.pid,signal.SIGTERM)
    try:proc.wait(timeout=30)
    except subprocess.TimeoutExpired:os.killpg(proc.pid,signal.SIGKILL);proc.wait(timeout=10)
   raise
with b.locked():
 assert not (old/'complete.json').exists()
 assert subprocess.check_output(['systemctl','show','baarcha-vps-archive-cold-nvme.service','-p','MainPID','--value'],text=True).strip()=='0'
 assert subprocess.check_output(['systemctl','show','baarcha-vps-storage-density-15.service','-p','MainPID','--value'],text=True).strip()=='0'
 first=json.loads((old/'online-precopy-20260926-01-verified.json').read_text());assert first['verified_copy']
 source=P(first['original_path']);assert source.is_symlink() and source.resolve()==P(first['destination']).resolve()
 assert not source.with_name(source.name+'.verified-nvme-before-20261009').exists()
 assert destroot.is_dir() and not destroot.is_symlink() and free('/')>500*1024**3
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
 ready();out.mkdir(mode=0o700)
 b.atomic(out/'intent.json',b.encoded({'resume_after':'writeback stalled observer; original sources preserved','rsync_kib_per_second':10240,'block_device_writeback_unthrottled':True,'at':time.time()}))
 results=[{'name':'online-precopy-20260926-01','verified_copy':True,'entries':first['entries'],'original_path_preserved':True}]
 try:
  for source in sources:
   assert source.is_dir() and not source.is_symlink();unreferenced(source);baseline=metadata(source)
   prior=old/(source.name+'-metadata.PRIVATE.json')
   if prior.exists():assert json.loads(prior.read_text())==baseline,'Original archive changed since interrupted copy'
   dest=destroot/source.name;assert not dest.is_symlink();dest.mkdir(mode=0o700,exist_ok=True)
   b.atomic(out/(source.name+'-metadata.PRIVATE.json'),b.encoded(baseline))
   # Pacing at the sender bounds dirty data without throttling kernel writeback.
   # Checksum existing files before skipping any completed interrupted-copy output.
   checked_run(['rsync','-aHAXS','--checksum','--bwlimit=10240','--numeric-ids',str(source)+'/',str(dest)+'/'],out/(source.name+'-copy.PRIVATE.log'),5400)
   verify=out/(source.name+'-verify.PRIVATE.log')
   checked_run(['rsync','-aHAXSn','--numeric-ids','--checksum','--delete','--itemize-changes',str(source)+'/',str(dest)+'/'],verify,3600)
   assert verify.stat().st_size==0,'Archive checksum/metadata mismatch'
   assert metadata(source)==baseline,'Archive changed during copy';unreferenced(source)
   retired=source.with_name(source.name+'.verified-nvme-before-20261009');assert not retired.exists()
   source.rename(retired)
   try:os.symlink(dest,source,target_is_directory=True)
   except BaseException:retired.rename(source);raise
   assert source.resolve()==dest.resolve()
   subprocess.run(['sync','-f',str(dest)],check=True,timeout=120)
   b.atomic(out/(source.name+'-verified.json'),b.encoded({'verified_copy':True,'original_path':str(source),'destination':str(dest),'entries':len(baseline),'source_path_preserved':True}))
   shutil.rmtree(retired)
   result={'name':source.name,'verified_copy':True,'entries':len(baseline),'original_path_preserved':True};results.append(result);print(json.dumps(result),flush=True)
  subprocess.run(['sync','-f','/mnt/nvme'],check=True,timeout=120);ready()
  before=json.loads((old/'intent.json').read_text())['nvme_free_before']
  result={'complete':True,'nvme_bytes_reclaimed':free('/mnt/nvme')-before,'destination':str(destroot),'archives':results,'all_data_preserved':True,'b200_contacted':False,'resumed_from':str(out),'at':time.time()}
  b.atomic(out/'complete.json',b.encoded(result));b.atomic(old/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
 except BaseException as error:
  b.atomic(out/'failed.json',b.encoded({'type':type(error).__name__,'reason':str(error)[:300],'at':time.time(),'remaining_original_sources_preserved':True}));raise
