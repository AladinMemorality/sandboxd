"""Read-only clone of one retained pause disk after its live metadata was removed."""
import datetime,hashlib,json,os,pathlib,re,shutil,subprocess,sys,urllib.request
P=pathlib.Path;os.umask(0o077)
SID='01M3MHG8KQHE8BM4JF8W7YAZ4Q';RID='b11475484a4544fbb48dad0660e7d0f7';SNAP='snap-733b1dac377d4e48b701ccef';TEMPLATE='tpl-3c4e83ebf6c641f293c0e816'
root=P(sys.argv[1]);assert root.parent==P('/data/baarcha-source-generations') and re.fullmatch(r'[0-9]{8}T[0-9]{6}Z',root.name)
assert os.geteuid()==0 and P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
scope=json.loads((root/'scope.json').read_text());assert scope=={'worker':'vps','bindings':[{'sandbox_id':SID,'runtime_id':RID,'worker_id':'vps'}]}
def observed():
 req=urllib.request.Request('http://127.0.0.1:8089/cube/sandbox/info?sandbox_id='+RID+'&instance_type=cubebox',headers={'X-Caller':'baarcha-controller'})
 value=json.load(urllib.request.urlopen(req,timeout=20));assert value['ret']['ret_code']==200 and len(value['data'])==1
 row=value['data'][0];assert row['sandbox_id']==RID and row['host_id']=='10.0.2.15' and row['status']==5 and row['template_id']==TEMPLATE
 assert row['annotations']['cube.master.pause.snapshot.id']==SNAP and row['labels']['sandboxd_id']==SID
 return row
before=observed();metadata=P('/data/cubelet/storage/xfs/pause-snapshots')/SNAP/'metadata'
catalog=json.loads((metadata/'catalog.json').read_text());spec=json.loads((metadata/'sandbox_spec.json').read_text());vm=json.loads((metadata/'metadata.json').read_text())
assert catalog['snapshot_id']==SNAP and catalog['backend']=='xfs' and catalog['kind']=='pause_snapshot'
assert spec['annotations']['cube.master.pause.snapshot.id']==SNAP and spec['labels']['sandboxd_id']==SID
assert vm['app_snapshot_container_id']==TEMPLATE+'_0' and vm['vm_res']['memory']==768
assert len(spec['containers'])==1
image=spec['containers'][0]['image']['image'];assert image=='rfs-4f45a7f572bb615720f5dcc0-3f7d58fa'
base=P('/data/cubebox-os-images')/image/(image+'.ext4')
assert base.stat().st_size==4026531840
with base.open('rb') as stream:assert hashlib.file_digest(stream,'sha256').hexdigest()==spec['containers'][0]['image']['annotations']['cube.master.rootfs.artifact.sha256']
volumes=P('/data/cubelet/storage/xfs/objects/volumes');matches=[p for p in volumes.rglob(catalog['rootfs_vol']) if p.is_file()]
assert len(matches)==1;disk=matches[0]
assert disk.stat().st_size==catalog['rootfs_size_bytes']==4294967296
assert vm['vm_res']['disks'][0]['size']>=disk.stat().st_size
assert shutil.disk_usage('/data').free>40*1024**3
tree=root/'tree';tree.mkdir(mode=0o700);meta=tree/'operator-metadata/cubebox';meta.mkdir(parents=True)
originals={};required=[]
def clone(path):
 assert path.is_file() and not path.is_symlink() and path.resolve()==path and path.stat().st_uid==0
 st=path.stat();identity=[st.st_dev,st.st_ino,st.st_size,st.st_mtime_ns];dest=tree/str(path).lstrip('/');dest.parent.mkdir(parents=True,exist_ok=True)
 subprocess.run(['cp','--preserve=mode,timestamps','--sparse=auto','--reflink=always',str(path),str(dest)],check=True,timeout=60)
 after=path.stat();assert identity==[after.st_dev,after.st_ino,after.st_size,after.st_mtime_ns]
 originals[str(path)]=identity;required.append(str(path).lstrip('/'))
for path in [base,disk,metadata/'metadata.json',metadata/'sandbox_spec.json',metadata/'catalog.json']:clone(path)
# This export descriptor is reconstructed from authenticated master identity and
# retained snapshot metadata, not represented as a successful cubecli inspection.
box={'ID':RID,'sandbox_id':RID,'Containers':{RID:{'cube_rootfs_info':{'pmem_file':str(base)},'config':{'image':image}}},'LocalRunTemplate':{'snapshot':{'snapshot':{'path':str(metadata)}}},'Annotations':{'cube.master.pause.snapshot.id':SNAP},'Labels':{'cube.master.pause.snapshot.id':SNAP},'operator_reconstructed_from_retained_snapshot':True}
(meta/(RID+'.json')).write_text(json.dumps(box))
assert observed()==before
(root/'resolved-coverage.json').write_text(json.dumps([{**scope['bindings'][0],'mode':'pause_snapshot','required_files':required,'covered':True,'changed_required':[]}]))
(root/'capture.json').write_text(json.dumps({'worker':'vps','captured_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'bindings':1,'files':len(required),'changed_during_capture':[],'consistency':'Unchanged retained paused disk; no failed memory state is booted.'}))
(root/'retained-snapshot-proof.PRIVATE.json').write_text(json.dumps({'snapshot_id':SNAP,'master_before':before,'source_identities':originals,'descriptor_reconstructed':True,'originals_preserved':True}))
print(json.dumps({'captured':1,'files':len(required),'changed_files':0,'retained_pause_disk':True}),flush=True)
