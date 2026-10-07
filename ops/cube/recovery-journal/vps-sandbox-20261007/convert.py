import pathlib,sys,json,os,stat,zipfile,hashlib
r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007');sys.path.insert(0,str(r));import archive_validate as av;import archive_convert as ac
os.umask(0o077)
expected=json.loads((r/'export-report.json').read_text())['archive_sha256']
allowed_roots={'.bashrc','.bash_logout','.bun','.cache','.config','.gitconfig','.npm','.npm-global','.profile','.baarcha-postgres'}
with (r/'home.tar').open('rb') as f:
 before=ac.source_stamp(f);report,index=av.validate(f,return_index=True);assert report['archive_sha256']==expected
 roots=set(x.split('/')[0] for x in index);retained=roots&{'.claude','.claude.json'}
 assert roots<=allowed_roots|{'workspace','.runtimed'}|retained
 ac.PRESERVE=tuple(sorted(roots-{'workspace','.runtimed'}-retained))
 index={name:entry for name,entry in index.items() if name.split('/')[0] not in retained}
 assert index['workspace/app/package.json']['kind']=='file' and index['workspace/app/index.html']['kind']=='file'
 stock=index.pop('workspace/.gitkeep',None)
 if stock is not None:assert stock['kind']=='file' and stock['size']==0
 plan,expanded=ac.conversion_plan(index)
 if stock is not None:plan.append(('home','workspace/.gitkeep',stock,stock))
 out=r/'converted';out.mkdir(mode=0o700)
 handles={g:(out/(g+'.zip')).open('xb') for g in ['app','home']};writers={g:zipfile.ZipFile(h,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=1) for g,h in handles.items()}
 try:
  for group,dest,entry,content in plan:
   kind='file' if entry['kind']=='hardlink' else entry['kind'];item=zipfile.ZipInfo(dest+('/' if kind=='dir' else ''),date_time=(1980,1,1,0,0,0));item.create_system=3;item.compress_type=zipfile.ZIP_DEFLATED
   bits=stat.S_IFDIR if kind=='dir' else stat.S_IFLNK if kind=='symlink' else stat.S_IFREG;item.external_attr=((bits|entry['mode'])<<16)|(0x10 if kind=='dir' else 0)
   size=len(entry['target'].encode()) if kind=='symlink' else content['size'];item.file_size=size
   with writers[group].open(item,'w') as z:
    if kind=='symlink':z.write(entry['target'].encode())
    elif kind=='file':
     f.seek(content['offset']);remaining=size
     while remaining:
      b=f.read(min(65536,remaining));assert b;z.write(b);remaining-=len(b)
  for z in writers.values():z.close()
  for h in handles.values():h.flush();os.fsync(h.fileno())
 finally:
  for z in writers.values():z.close()
  for h in handles.values():h.close()
 task='01M3HH7R48XW80033ZNP625BZ3'
 with zipfile.ZipFile(out/'history.zip','x',compression=zipfile.ZIP_DEFLATED) as history:
  for name in ['events.jsonl','result.json']:
   entry=index['.runtimed/tasks/'+task+'/'+name]
   assert entry['kind']=='file' and entry['size']<=32*1024*1024
   f.seek(entry['offset']);data=f.read(entry['size']);assert len(data)==entry['size']
   if name=='result.json':
    result=json.loads(data);assert result['id']==task and result['status']=='succeeded'
   item=zipfile.ZipInfo(task+'/'+name,date_time=(1980,1,1,0,0,0));item.create_system=3;item.compress_type=zipfile.ZIP_DEFLATED;item.external_attr=(stat.S_IFREG|0o644)<<16;history.writestr(item,data)
 f.seek(0);assert hashlib.file_digest(f,'sha256').hexdigest()==expected and ac.source_stamp(f)==before
 manifest={'version':2,'entries':[{'path':'workspace/app','disposition':'separate'},{'path':'.runtimed','disposition':'separate'},*([{'path':'workspace/.gitkeep','disposition':'stock'}] if stock is not None else []),*[{'path':p,'disposition':'preserve'} for p in ac.PRESERVE],*[{'path':p,'disposition':'retained','reason':'Provider identity remains in the private source archive; use a fresh authenticated agent session.'} for p in sorted(retained)]]}
 (out/'home-manifest.json').write_text(json.dumps(manifest))
 result={'archive_sha256':expected,'expanded_bytes':expanded,'runtime_identity_separate':True,'source_original_unchanged':True,'extracted':False,'preserved_home_roots':list(ac.PRESERVE),'provider_identity_retained_in_source':sorted(retained)}
 for n in ['app','home','history']:
  with (out/(n+'.zip')).open('rb') as z:result[n+'_sha256']=hashlib.file_digest(z,'sha256').hexdigest()
 (out/'conversion.json').write_text(json.dumps(result));print(json.dumps({'converted':True,'expanded_bytes':expanded,'source_original_unchanged':True}))
