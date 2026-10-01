import pathlib,os,json,subprocess,hashlib,fcntl,shutil,time
os.umask(0o077)
base=pathlib.Path('/usr/local/services/cubetoolbox/cubebox_os_image');dest=pathlib.Path('/data/cubebox-os-images');backup=base.with_name('cubebox_os_image-moving-20260929');receipt=pathlib.Path('/data/cube-recovery/myhometroc-20260929/image-move.json')
def sha(p):
 with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
with open('/run/lock/cube-operator-acceptance.lock','a') as lock:
 fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 assert not base.is_symlink() and not dest.exists() and not backup.exists()
 tasks=subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list','-q'],text=True).split();assert not tasks
 entries=list(base.rglob('*'));assert all(p.is_dir() or p.is_file() for p in entries);assert not any(p.is_symlink() for p in entries)
 files=[p for p in entries if p.is_file()];ids={(p.stat().st_dev,p.stat().st_ino) for p in files}
 for proc in pathlib.Path('/proc').iterdir():
  if not proc.name.isdigit():continue
  try:fds=list((proc/'fd').iterdir())
  except OSError:continue
  for fd in fds:
   try:s=fd.stat()
   except OSError:continue
   assert (s.st_dev,s.st_ino) not in ids,'image has an active process handle'
 assert shutil.disk_usage('/data').free>sum(p.stat().st_blocks*512 for p in files)+(48<<30)
 print('copying immutable images',len(files),flush=True)
 subprocess.run(['cp','-a','--sparse=always',str(base),str(dest)],check=True)
 subprocess.run(['sync','-f',str(dest)],check=True)
 hashes={}
 for p in files:
  rel=str(p.relative_to(base));before=p.stat();h=sha(p);assert sha(dest/rel)==h;after=p.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns);hashes[rel]=h
 receipt.write_text(json.dumps({'copied_verified':True,'files':len(files),'sha256':hashes,'at':time.time()}));print('all image hashes verified',flush=True)
 fstab=pathlib.Path('/etc/fstab');before=fstab.read_text();line=str(dest)+' '+str(base)+' none bind 0 0\n';assert str(base) not in before
 pathlib.Path('/data/cube-recovery/myhometroc-20260929/fstab-before').write_text(before)
 base.rename(backup);base.mkdir(mode=0o755)
 try:subprocess.run(['mount','--bind',str(dest),str(base)],check=True)
 except BaseException:base.rmdir();backup.rename(base);raise
 assert subprocess.check_output(['findmnt','-n','-o','TARGET','--target',str(base)],text=True).strip()==str(base)
 subprocess.run(['sync','-f',str(base.parent)],check=True)
 fstab.write_text(before.rstrip()+'\n# Cube immutable OS images reside on the NVMe data volume.\n'+line)
 with fstab.open('rb') as f:os.fsync(f.fileno())
 subprocess.run(['systemctl','daemon-reload'],check=True)
 for p in files:
  rel=str(p.relative_to(base));assert sha(base/rel)==hashes[rel]
 # The only removal is the old verified copy; every byte remains at its original path through the bind mount.
 shutil.rmtree(backup)
 subprocess.run(['sync','-f',str(base.parent)],check=True)
 receipt.write_text(json.dumps({'moved_verified':True,'files':len(files),'sha256':hashes,'at':time.time(),'original_paths_preserved':True}))
 print(subprocess.check_output(['df','-h','/','/data'],text=True),flush=True)
