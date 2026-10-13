#!/usr/bin/env python3
"""Export saved homes without installed dependencies inside a networkless VM."""
import concurrent.futures,datetime,hashlib,json,os,pathlib,posixpath,re,shlex,sys,time,tarfile
import guestfs
P=pathlib.Path;os.umask(0o077)
kernel_env=P('/data/cube-source-backup-tools/kernel-env.json')
if kernel_env.exists():os.environ.update(json.loads(kernel_env.read_text()))
capture=P(sys.argv[1]);assert capture.parent==P('/data/baarcha-source-generations') and re.fullmatch(r'[0-9]{8}T[0-9]{6}Z',capture.name)
tree=capture/'tree';out=capture/'exports';out.mkdir(mode=0o700,exist_ok=True)
scope=json.loads((capture/'scope.json').read_text());rows=json.loads((capture/'resolved-coverage.json').read_text())
HANDLE=None;DEVICES={}
def save(p,x):
 tmp=p.with_suffix(p.suffix+'.tmp');tmp.write_text(json.dumps(x,indent=2)+'\n');os.replace(tmp,p)
def sha(p):
 h=hashlib.sha256()
 with p.open('rb') as f:
  for block in iter(lambda:f.read(4*1024*1024),b''):h.update(block)
 return h.hexdigest()
def local(path):
 assert isinstance(path,str) and path.startswith('/') and '..' not in P(path).parts
 p=tree/path.lstrip('/')
 for _ in range(12):
  cursor=tree;parts=p.relative_to(tree).parts;changed=False
  for i,part in enumerate(parts):
   cursor=cursor/part
   if cursor.is_symlink():
    target=str(cursor.readlink());virtual=target if target.startswith('/') else '/'+str(cursor.parent.relative_to(tree))+'/'+target
    p=(tree/posixpath.normpath(virtual).lstrip('/')).joinpath(*parts[i+1:]);changed=True;break
  if not changed:return p
 raise ValueError('reference symlink loop')
def inputs(row):
 sid=row['sandbox_id'];rid=row['runtime_id'];assert re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}',sid)
 assert row['covered'];box=json.loads((tree/'operator-metadata/cubebox'/(rid+'.json')).read_text())
 containers=box.get('Containers') or box.get('ContainersMap',{}).get('ContainerMap',{})
 assert len(containers)==1 and rid in containers
 container=containers[rid];lower_path=container['cube_rootfs_info']['pmem_file'];lower=local(lower_path)
 roots=[p for p in row['required_files'] if ('/sb-'+rid+'-rootfs-gen') in p] if row['mode']=='current_disk' else [p for p in row['required_files'] if p.endswith('tpl-'+box['Annotations']['cube.master.pause.snapshot.id']+'-rootfs')]
 assert len(roots)==1,(sid,'ambiguous current root')
 disk=tree/roots[0]
 template=box['LocalRunTemplate']['snapshot']['snapshot'];tm=json.loads((local(template['path'])/'metadata.json').read_text())
 upper_id=tm['app_snapshot_container_id'];assert re.fullmatch('tpl-[a-z0-9]+_0',upper_id)
 return box,container,disk,lower,upper_id
def export(row):
 sid=row['sandbox_id'];rid=row['runtime_id'];dest=out/sid;dest.mkdir(mode=0o700,exist_ok=True)
 if (dest/'receipt.json').exists() and (dest/'dependency-audit.json').exists():
  prior=json.loads((dest/'receipt.json').read_text())
  if 'preserved_dependency_files' in prior:return prior
 box,container,disk,lower,upper_id=inputs(row)
 originals={str(p):[p.stat().st_ino,p.stat().st_size,p.stat().st_mtime_ns] for p in [disk,lower]}
 save(dest/'progress.json',dict(phase='mounting_saved_disk',at=time.time()))
 g=HANDLE;devices=['/dev/disk/guestfs/upper','/dev/disk/guestfs/base']
 try:
  assert all(re.fullmatch('/dev/disk/guestfs/[a-z]+',d) for d in devices)
  g.debug('sh',['set -eu; mkdir -p /tmp/saved-upper /tmp/saved-base; mount -t ext4 -o ro,nosuid,nodev,noexec '+devices[0]+' /tmp/saved-upper; mount -t ext4 -o ro,noload,nosuid,nodev,noexec '+devices[1]+' /tmp/saved-base; modprobe overlay; mount -t overlay overlay -o ro,nosuid,nodev,noexec,metacopy=on,redirect_dir=on,lowerdir=/tmp/saved-upper/disk/'+upper_id+'/upper:/tmp/saved-base /sysroot'])
  assert g.is_dir('/home/sandbox')
  exclusions=['*/node_modules','node_modules','*/__pycache__','__pycache__',
      './.npm/_cacache','./.local/share/pnpm/store','./.pnpm-store','./.cache/pnpm','./.cache/pip','./.cache/yarn','./.cache/node-gyp','./.cache/ms-playwright',
      '*/.next/cache','*/.vite','*/.turbo','*/.parcel-cache','./.runtimed/sock',
      './.baarcha-postgres/run/.s.PGSQL.*','./.myhometroc/socket/.s.PGSQL.*',
      # PostgreSQL resets these cumulative counters after crash recovery.
      # Preserve relations, WAL, configuration and all unrelated user paths.
      './.baarcha-postgres/data/pg_stat/pgstat.stat',
      './.baarcha-postgres/data/pg_stat/pgstat.tmp']
  for name in ['.venv','venv']:
   if g.is_file('/home/sandbox/workspace/app/'+name+'/pyvenv.cfg'):exclusions.append('./workspace/app/'+name)
  project='/home/sandbox/workspace/app'
  manifest={};lockfiles=[]
  if g.is_file(project+'/package.json'):
   manifest=json.loads(g.read_file(project+'/package.json'));save(dest/'package.json',manifest)
  for name in ['package-lock.json','npm-shrinkwrap.json','pnpm-lock.yaml','yarn.lock','bun.lock','bun.lockb','requirements.txt','pyproject.toml','poetry.lock','uv.lock']:
   if g.is_file(project+'/'+name):lockfiles.append(name)
  # Keep authored patches and local dependency sources inside the saved home.
  exceptions=[]
  for group in ['dependencies','devDependencies','optionalDependencies']:
   for name,version in manifest.get(group,{}).items():
    if isinstance(version,str) and version.startswith(('file:','link:','workspace:','git+','http:','https:')):exceptions.append(dict(package=name,reference=version))
  if manifest and not lockfiles:exceptions.append(dict(reason='No dependency lockfile found'))
  audit=dict(method='Files newer than npm installed-package metadata; heuristic, not a registry byte comparison',status='no_npm_install_marker',preserved_files=[])
  marker=project+'/node_modules/.package-lock.json'
  if g.is_file(marker):
   count=int(g.debug('sh',["set -eu; cd /sysroot/home/sandbox/workspace/app/node_modules; find . -type d \\( -name .vite -o -name .cache \\) -prune -o -type f ! -name .package-lock.json -newer .package-lock.json -printf x | wc -c"]).strip());assert count<100000
   audit.update(status='checked_install_timestamps')
   if count:
    g.debug('sh',["set -eu; cd /sysroot/home/sandbox/workspace/app/node_modules; find . -type d \\( -name .vite -o -name .cache \\) -prune -o -type f ! -name .package-lock.json -newer .package-lock.json -print0 | tar --null --verbatim-files-from --no-recursion --numeric-owner -czf /tmp/dependency-modifications.tar.gz -T -"])
    g.debug('sh',['test ! -L /sysroot/tmp; mount --bind /tmp /sysroot/tmp'])
    try:g.download('/tmp/dependency-modifications.tar.gz',str(dest/'dependency-modifications.tar.gz'))
    finally:g.debug('sh',['umount /sysroot/tmp'])
    with tarfile.open(dest/'dependency-modifications.tar.gz','r:gz') as archive:audit['preserved_files']=[m.name for m in archive if m.isfile()]
    assert len(audit['preserved_files'])==count
    audit['archive_sha256']=sha(dest/'dependency-modifications.tar.gz');audit['archive_bytes']=(dest/'dependency-modifications.tar.gz').stat().st_size
  save(dest/'dependency-audit.json',audit)
  save(dest/'progress.json',dict(phase='exporting_home',at=time.time()))
  archive=dest/'home.tar.gz'
  if not (archive.exists() and (dest/'receipt.json').exists()):
   partial=dest/'home.tar.gz.partial'
   g.tar_out('/home/sandbox',str(partial),compress='gzip',numericowner=True,excludes=exclusions,xattrs=True,acls=True)
   os.replace(partial,archive)
  # These metadata files describe reconstruction but contain no installed code.
  recipe=dict(export_version=2, disk_attachment='Isolated VM with two labeled read-only input disks', sandbox_id=sid,runtime_id=rid,worker=scope['worker'],base_image=container['config']['image'],
      source_capture=json.loads((capture/'capture.json').read_text())['captured_at'],
      lockfiles=lockfiles,dependency_exceptions=exceptions,excluded_patterns=exclusions,
      consistency='crash-consistent' if row['changed_required'] else 'unchanged saved disk',
      user_data_scope='Entire merged /home/sandbox except listed dependencies, caches, and ephemeral sockets',
      reinstall_note='Use the saved package manager and lockfile. Keep local packages/patches and review non-registry dependencies. No dependency install scripts executed during backup.')
  save(dest/'recipe.json',recipe)
  assert all([p.stat().st_ino,p.stat().st_size,p.stat().st_mtime_ns]==originals[str(p)] for p in [disk,lower])
  receipt=dict(sandbox_id=sid,runtime_id=rid,worker=scope['worker'],bytes=archive.stat().st_size,sha256=sha(archive),
      exported_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),lockfiles=lockfiles,exceptions=len(exceptions),dependency_audit=audit['status'],preserved_dependency_files=len(audit['preserved_files']))
  save(dest/'receipt.json',receipt);save(dest/'progress.json',dict(phase='exported',at=time.time()))
  return receipt
 finally:
  g.debug('sh',['umount /sysroot; umount /tmp/saved-base; umount /tmp/saved-upper'])
def main():
 global HANDLE,DEVICES
 selected=rows
 if len(sys.argv)>2:selected=[r for r in rows if r['sandbox_id']==sys.argv[2]];assert len(selected)==1
 results=[];failures=[]
 for row in selected:
  sid=row['sandbox_id'];dest=out/sid
  if (dest/'receipt.json').exists() and (dest/'dependency-audit.json').exists() and 'preserved_dependency_files' in json.loads((dest/'receipt.json').read_text()):
   results.append(json.loads((dest/'receipt.json').read_text()));continue
  _,_,disk,lower,_=inputs(row)
  g=guestfs.GuestFS(python_return_dict=True);g.set_backend('direct');g.set_network(False);g.set_memsize(1024);g.set_smp(1)
  g.add_drive_opts(str(disk),readonly=True,format='raw',label='upper')
  g.add_drive_opts(str(lower),readonly=True,format='raw',label='base')
  try:
   g.launch();bylabel=g.list_disk_labels();DEVICES={str(disk):bylabel['upper'],str(lower):bylabel['base']};HANDLE=g
   result=export(row);results.append(result);print(json.dumps(dict(sandbox_id=sid,exported=True,bytes=result['bytes'])),flush=True)
   (dest/'failure.json').unlink(missing_ok=True)
  except Exception as e:
   dest.mkdir(mode=0o700,exist_ok=True)
   error=dict(sandbox_id=sid,error=type(e).__name__,message=str(e)[:1000]);save(dest/'failure.json',error);failures.append(error)
   print(json.dumps(error),flush=True)
  finally:g.close()
  save(out/'progress.json',dict(worker=scope['worker'],exported=len(results),failed=len(failures),selected=len(selected),bytes=sum(r['bytes'] for r in results),at=time.time()))
 save(out/'failures.json',failures)
 save(out/'receipts.json',results)
 if failures:raise RuntimeError(str(len(failures))+" sandbox exports failed; original captured disks retained")
if __name__=='__main__':main()
