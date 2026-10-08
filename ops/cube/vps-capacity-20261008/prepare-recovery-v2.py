"""Convert a verified VPS source backup into private same-owner import artifacts.

No worker access or database mutation. Keep provider identities in the original
backup; restore task history separately from live supervisor credentials.
"""
import gzip,hashlib,importlib.util,json,os,pathlib,sqlite3,stat,sys,zipfile
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008');sys.path.insert(0,str(root/'recovery-tools'))
import archive_validate as av
import archive_convert as ac
import move_project_worker as transport
sid=sys.argv[1];assert len(sid)==26 and sid.isalnum()
backup=P('/var/backups/baarcha-sandboxes/20261008T1440Z');bindings=json.loads((backup/'bindings.json').read_text());binding=next(x for x in bindings if x['sandbox_id']==sid)
source=backup/'source-backup-v2'/binding['worker_id']/sid;verified=json.loads((source/'verified.json').read_text())
with (source/'home.tar.gz').open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==verified['sha256']
a=sqlite3.connect('file:'+str(backup/'controller.sqlite')+'?mode=ro',uri=True);db=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)
q='select task_id,status,prompt,result_json from task where sandbox_id=? order by task_id';tasks=db.execute(q,(sid,)).fetchall();assert tasks==a.execute(q,(sid,)).fetchall() and all(t[1] in ('succeeded','failed','cancelled') for t in tasks)
assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()[0]==binding['runtime_id']
a.close();db.close()
out=root/'recovery-prepared-v2'/sid;out.mkdir(mode=0o700,parents=True,exist_ok=False)
raw=out/'home.tar';total=0
with gzip.open(source/'home.tar.gz','rb') as src,raw.open('xb') as dest:
 while data:=src.read(4*1024**2):
  total+=len(data);assert total<=20*1024**3;dest.write(data)
with raw.open('rb') as f:
 report,index=av.validate(f,return_index=True)
 roots={x.split('/')[0] for x in index}
 retained={x for x in roots if x in {'.claude','.claude.json','.codex','.gemini','.ssh','.aws','.azure','.gnupg','.kube','.docker','.pki','.git-credentials','.netrc','.npmrc'} or x.startswith('.claude.json.')}
 siblings={n for n in index if n.startswith('workspace/') and n.count('/')==1 and n not in {'workspace/app','workspace/.gitkeep'}}
 ac.PRESERVE=tuple(sorted((roots-{'workspace','.runtimed'}-retained)|siblings))
 index={n:v for n,v in index.items() if n.split('/')[0] not in retained}
 assert index['workspace/app/package.json']['kind']=='file'
 stock=index.pop('workspace/.gitkeep',None)
 if stock:assert stock['kind']=='file' and stock['size']==0
 plan,expanded=ac.conversion_plan(index)
 if stock:plan.append(('home','workspace/.gitkeep',stock,stock))
 paths={g:out/(g+'.zip') for g in ['workspace','home']}
 with zipfile.ZipFile(paths['workspace'],'x',compression=zipfile.ZIP_DEFLATED,compresslevel=1) as app,zipfile.ZipFile(paths['home'],'x',compression=zipfile.ZIP_DEFLATED,compresslevel=1) as home:
  writers={'app':app,'home':home}
  for group,dest,entry,content in plan:
   kind='file' if entry['kind']=='hardlink' else entry['kind'];item=zipfile.ZipInfo(dest+('/' if kind=='dir' else ''),date_time=(1980,1,1,0,0,0));item.create_system=3;item.compress_type=zipfile.ZIP_DEFLATED
   bits=stat.S_IFDIR if kind=='dir' else stat.S_IFLNK if kind=='symlink' else stat.S_IFREG;item.external_attr=((bits|entry['mode'])<<16)|(0x10 if kind=='dir' else 0)|(1 if not entry['mode']&0o200 else 0)
   size=len(entry['target'].encode()) if kind=='symlink' else content['size'];item.file_size=size
   with writers[group].open(item,'w') as z:
    if kind=='symlink':z.write(entry['target'].encode())
    elif kind=='file':
     f.seek(content['offset']);remaining=size
     while remaining:
      data=f.read(min(65536,remaining));assert data;z.write(data);remaining-=len(data)
 with zipfile.ZipFile(out/'history.zip','x',compression=zipfile.ZIP_DEFLATED) as history:
  for task,status,_,_ in tasks:
   for name in ['events.jsonl','result.json']:
    entry=index['.runtimed/tasks/'+task+'/'+name];assert entry['kind']=='file' and entry['size']<=32*1024**2
    f.seek(entry['offset']);data=f.read(entry['size']);assert len(data)==entry['size']
    if name=='result.json':v=json.loads(data);assert v['id']==task and v['status']==status
    item=zipfile.ZipInfo(task+'/'+name,date_time=(1980,1,1,0,0,0));item.create_system=3;item.compress_type=zipfile.ZIP_DEFLATED;item.external_attr=(stat.S_IFREG|0o644)<<16;history.writestr(item,data)
manifest={'version':2,'entries':[{'path':'workspace/app','disposition':'separate'},{'path':'.runtimed','disposition':'separate'},*([{'path':'workspace/.gitkeep','disposition':'stock'}] if stock else []),*[{'path':p,'disposition':'preserve'} for p in ac.PRESERVE],*[{'path':p,'disposition':'retained','reason':'Provider identity remains in the original private backup; use a fresh authenticated agent session.'} for p in sorted(retained)]]}
result={'sandbox_id':sid,'runtime_id':binding['runtime_id'],'home_manifest':manifest,'task_ids':[t[0] for t in tasks],'source_archive_sha256':verified['sha256'],'artifacts':{}}
for role in ['workspace','home','history']:
 p=out/(role+'.zip');result['artifacts'][role]={'sha256':transport.digest(p),'archive_bytes':p.stat().st_size,'local_path':str(p)}
(out/'export-result.PRIVATE.json').write_text(json.dumps(result));print(json.dumps({'prepared':True,'sandbox_id':sid,'tasks':len(tasks),'expanded_bytes':expanded,'provider_identity_retained_roots':len(retained)}))
