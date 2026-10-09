"""Move verified inactive transfer archives to HDD; preserve their original paths."""
import hashlib,importlib.util,json,os,pathlib,shutil,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'cold-archive-move';destroot=P('/var/backups/baarcha-cold-runtime/20261009')
names=['online-precopy-20260926-01','migration-archives','fleet-artifact-stage'];sources=[P('/mnt/nvme/baarcha-cube')/n for n in names]
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
def below(value,source):return value==str(source) or value.startswith(str(source)+'/')
def unreferenced(source):
 ids=subprocess.check_output(['docker','ps','-aq'],text=True).split()
 for container in json.loads(subprocess.check_output(['docker','inspect',*ids])):
  for mount in container.get('Mounts',[]):
   path=P(mount['Source']);assert not (path==source or path in source.parents or source in path.parents),'Container mount references archive'
 for proc in P('/proc').glob('[0-9]*'):
  try:
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
with b.locked():
 assert json.loads((root/'real-preview-density-50-balanced/cleanup.json').read_text())['complete']
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
 out.mkdir(mode=0o700);destroot.mkdir(mode=0o700,parents=True,exist_ok=False)
 assert os.stat(destroot).st_dev==os.stat('/').st_dev and os.stat(destroot).st_dev!=os.stat('/mnt/nvme').st_dev and free('/')>500*1024**3
 before=free('/mnt/nvme');results=[]
 b.atomic(out/'intent.json',b.encoded({'sources':[str(x) for x in sources],'destination':str(destroot),'nvme_free_before':before,'source_data_preserved':True}))
 for source in sources:
  assert source.is_dir() and not source.is_symlink();unreferenced(source);baseline=metadata(source);dest=destroot/source.name;dest.mkdir(mode=0o700)
  b.atomic(out/(source.name+'-metadata.PRIVATE.json'),b.encoded(baseline))
  with (out/(source.name+'-copy.PRIVATE.log')).open('wb') as log:
   subprocess.run(['rsync','-aHAXS','--numeric-ids',str(source)+'/',str(dest)+'/'],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=3600)
  verify=subprocess.run(['rsync','-aHAXSn','--numeric-ids','--checksum','--delete','--itemize-changes',str(source)+'/',str(dest)+'/'],capture_output=True,timeout=3600)
  b.atomic(out/(source.name+'-verify.PRIVATE.log'),verify.stdout+verify.stderr);assert verify.returncode==0 and not verify.stdout and not verify.stderr,'Archive checksum/metadata mismatch'
  assert metadata(source)==baseline,'Archive changed during copy';unreferenced(source)
  retired=source.with_name(source.name+'.verified-nvme-before-20261009');assert not retired.exists()
  source.rename(retired)
  try:os.symlink(dest,source,target_is_directory=True)
  except BaseException:retired.rename(source);raise
  assert source.resolve()==dest.resolve()
  subprocess.run(['sync','-f',str(dest)],check=True,timeout=60)
  b.atomic(out/(source.name+'-verified.json'),b.encoded({'verified_copy':True,'original_path':str(source),'destination':str(dest),'entries':len(baseline),'source_path_preserved':True}))
  shutil.rmtree(retired)
  result={'name':source.name,'verified_copy':True,'entries':len(baseline),'original_path_preserved':True};results.append(result);print(json.dumps(result),flush=True)
 subprocess.run(['sync','-f','/mnt/nvme'],check=True,timeout=60)
 result={'complete':True,'nvme_bytes_reclaimed':free('/mnt/nvme')-before,'destination':str(destroot),'archives':results,'all_data_preserved':True,'b200_contacted':False,'at':time.time()};b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
