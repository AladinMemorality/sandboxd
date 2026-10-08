#!/usr/bin/env python3
"""Clone only the VPS disks needed for offline home export; never wake a guest."""
import datetime,json,os,pathlib,re,shutil,subprocess,sys
P=pathlib.Path;os.umask(0o077)
root=P(sys.argv[1])
assert root.parent==P('/data/baarcha-source-generations') and re.fullmatch(r'[0-9]{8}T[0-9]{6}Z',root.name)
assert os.geteuid()==0 and not root.is_symlink()
scope=json.loads((root/'scope.json').read_text());assert scope['worker']=='vps'
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
assert shutil.disk_usage('/data').free>40*1024**3
tree=root/'tree';tree.mkdir(mode=0o700,exist_ok=False)
meta=tree/'operator-metadata';(meta/'cubebox').mkdir(parents=True,mode=0o700)
shutil.copy2(root/'scope.json',meta/'scope.json')
def run(args):return subprocess.check_output(args,stderr=subprocess.PIPE,timeout=90)
raw=run(['cubecli','storage','ls','--raw']);(meta/'storage.txt').write_bytes(raw)
storage={}
for line in raw.decode().splitlines():
    if '\t{' in line:
        value=json.loads(line.split('\t',1)[1]);storage[value['sandboxID']]=value
volumes=P('/data/cubelet/storage/xfs/objects/volumes')
copied={};changed=[]
def identity(path):
    s=path.stat();return [s.st_dev,s.st_ino,s.st_size,s.st_mtime_ns]
def clone(path):
    path=P(path);assert path.is_absolute() and '..' not in path.parts
    assert str(path).startswith(('/data/cubelet/','/usr/local/services/cubetoolbox/cubebox_os_image/'))
    assert path.is_file() and not path.is_symlink()
    virtual=str(path).lstrip('/')
    if virtual in copied:return virtual
    original=identity(path);dest=tree/virtual;dest.parent.mkdir(parents=True,exist_ok=True)
    # Resolve the approved image-cache alias; all writable roots are on XFS.
    option='--reflink=always' if path.resolve().is_relative_to('/data') else '--reflink=auto'
    run(['cp','--preserve=mode,timestamps','--sparse=auto',option,str(path),str(dest)])
    assert dest.stat().st_size==original[2]
    if identity(path)!=original:changed.append('/'+virtual)
    copied[virtual]=original;return virtual
def volume(name):
    assert re.fullmatch(r'[a-zA-Z0-9_.-]+',name)
    matches=[p for p in volumes.rglob(name) if p.is_file()]
    assert len(matches)==1,'snapshot root absent or ambiguous';return matches[0]
rows=[]
for row in scope['bindings']:
    rid=row['runtime_id'];assert re.fullmatch('[a-f0-9]{32}',rid)
    assert re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}',row['sandbox_id'])
    box=json.loads(run(['cubecli','cubebox','inspect',rid]));assert box['ID']==rid and box['sandbox_id']==rid
    (meta/'cubebox'/(rid+'.json')).write_text(json.dumps(box))
    cs=box.get('Containers') or box.get('ContainersMap',{}).get('ContainerMap',{})
    assert len(cs)==1 and rid in cs
    base=P(cs[rid]['cube_rootfs_info']['pmem_file'])
    required=[clone(base)]
    template=P(box['LocalRunTemplate']['snapshot']['snapshot']['path'])
    required.extend(clone(p) for p in template.iterdir() if p.is_file())
    current=storage.get(rid,{}).get('volumes',[])
    if current:
        candidates=[P(v['file_path']) for v in current if '/sb-'+rid+'-rootfs-gen' in v['file_path']]
        assert len(candidates)==1;disk=candidates[0];mode='current_disk'
    else:
        snapshot=box['Annotations']['cube.master.pause.snapshot.id']
        assert snapshot==box['Labels']['cube.master.pause.snapshot.id'] and re.fullmatch('snap-[a-f0-9]+',snapshot)
        metadata=P('/data/cubelet/storage/xfs/pause-snapshots')/snapshot/'metadata'
        catalog=json.loads((metadata/'catalog.json').read_text());assert catalog['snapshot_id']==snapshot
        required.extend(clone(p) for p in metadata.iterdir() if p.is_file())
        disk=volume(catalog['rootfs_vol']);mode='pause_snapshot'
    required.append(clone(disk))
    # A changed provider identity or disk generation requires another capture.
    after=json.loads(run(['cubecli','cubebox','inspect',rid]));assert after['ID']==rid
    assert after.get('Annotations',{}).get('cube.master.pause.snapshot.id')==box.get('Annotations',{}).get('cube.master.pause.snapshot.id')
    rows.append({**row,'mode':mode,'required_files':required,'covered':True,'changed_required':[p for p in required if '/'+p in changed]})
report={'worker':'vps','captured_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'bindings':len(rows),'files':len(copied),'changed_during_capture':changed,'consistency':'Per-file atomic clones; no running-memory or transactional database consistency guarantee.'}
(root/'resolved-coverage.json').write_text(json.dumps(rows))
(root/'capture.json').write_text(json.dumps(report))
print(json.dumps({'captured':len(rows),'files':len(copied),'changed_files':len(changed)}),flush=True)
