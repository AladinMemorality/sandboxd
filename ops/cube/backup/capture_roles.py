#!/usr/bin/env python3
"""Prepare closed recovery roles only. Never stops, starts or repairs a source."""
import argparse,contextlib,fcntl,hashlib,json,os,pathlib,re,resource,shutil,sqlite3,stat,subprocess,sys,time,urllib.parse,urllib.request
import cold_pair
Path=pathlib.Path
LOCKS=('/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock')
DB=Path('/var/lib/sandboxd/state/sandboxd.db')
PG18='docker.io/library/postgres@sha256:9e73daeb439141c2b11eea2463f5f1a3b269fd90d897b41cddb7cb440f21aa5d'
PREVIEW_KEY=Path('/var/lib/sandboxd/cube-preview-auth.env')
NODE='/opt/baarcha/node22/bin/node'
REQUIRED_WRITERS={f'baarcha-{name}.{kind}' for name in ('project-env-apply','classroom-egress','fennec-meet-egress') for kind in ('timer','service')}
ROLE_NAMES={'controller-config','worker-config','worker-launch','rollback-extra','library-extra'}
PAIR_RESERVE=568*(1<<30)
CAPTURE_FDS=()

def need(ok,message):
 if not ok:raise RuntimeError(message)
def sha(p):return cold_pair.digest(p)
def canonical(x):return json.dumps(x,sort_keys=True,separators=(',',':')).encode()
def private(p,directory=False):
 p=Path(p);s=p.lstat();need(p.is_absolute() and p.resolve()==p and s.st_uid==0 and stat.S_IMODE(s.st_mode)==(0o700 if directory else 0o600),'Unsafe private operator path');need(p.is_dir() if directory else stat.S_ISREG(s.st_mode),'Unexpected operator path type');return p
def write(p,data):
 fd=os.open(p,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'w') as f:json.dump(data,f,sort_keys=True,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())
def run(args,timeout=15,max_bytes=None):
 kwargs={} if max_bytes is None else {"preexec_fn":lambda:resource.setrlimit(resource.RLIMIT_FSIZE,(max_bytes,max_bytes))}
 p=subprocess.run(args,capture_output=True,timeout=timeout,pass_fds=CAPTURE_FDS,**kwargs);need(p.returncode==0,'Read-only observation failed: '+Path(args[0]).name);return p.stdout

def validate_config(c):
 need(c.get('version')==1,'Unsupported role config')
 need(set(c['role_paths'])==ROLE_NAMES,'Exact recovery role set required')
 need(REQUIRED_WRITERS<=set(c['stopped_writer_units']),'Known direct writers omitted')
 need(re.fullmatch(r's-[0-9a-hjkmnp-tv-z]{26}-[0-9]{1,5}\.preview\.65\.108\.225\.153\.sslip\.io',c['preview_host']),'Reviewed existing preview hostname required')
 need(type(c['role_byte_budget']) is int and c['role_byte_budget']>0,'Positive role byte budget required')
 need(type(c['minimum_free_bytes']) is int and c['minimum_free_bytes']>=PAIR_RESERVE+2*c['role_byte_budget'],'Reserve must include role staging and later role copy')
 need(c['reviewed_files'] and str(PREVIEW_KEY) in c['reviewed_files'],'Preview auth file must be pinned')
 for path,digest in c['reviewed_files'].items():
  need(Path(path).is_absolute() and re.fullmatch('[0-9a-f]{64}',digest),'Invalid reviewed input identity')
 need(re.fullmatch('[0-9a-f]{64}',c['controller_env_sha256']) and re.fullmatch('[0-9a-f]{64}',c['reviewed_caddy_sha256']),'Configuration digests required')
 for role,paths in c['role_paths'].items():
  need(isinstance(paths,list) and all(isinstance(p,str) and Path(p).is_absolute() for p in paths),'Absolute role inputs required')
  if not role.endswith('-extra'):need(paths,'Required role is empty')
 need(all(any(Path(p)==Path(root) or Path(root) in Path(p).parents for root in c['role_paths']['controller-config']+c['role_paths']['worker-config']+c['role_paths']['worker-launch']) for p in c['reviewed_files']),'Pinned configuration omitted from archives')
 need(len({x['path'] for x in c['ephemeral_sockets']})==len(c['ephemeral_sockets']),'Duplicate socket review')

def verify_inputs(c):
 for p,digest in c['reviewed_files'].items():
  path=Path(p);info=path.lstat()
  need(path.resolve()==path and stat.S_ISREG(info.st_mode) and info.st_uid==0 and not(info.st_mode&0o022),'Unsafe reviewed configuration file')
  if path==PREVIEW_KEY:private(path)
  need(sha(path)==digest,'Reviewed configuration changed')

def route_fence(c):
 for host,path,expected in [('baarcha.tn',p,503) for p in ('/api/projects','/api/bridge','/api/apps/not-an-app/preview','/api/tools/call')]+[(c['preview_host'],'/',503),('baarcha.tn','/',200)]:
  code=run(['curl','--silent','--show-error','--noproxy','*','--max-time','5','--resolve',host+':443:127.0.0.1','--output','/dev/null','--write-out','%{http_code}','https://'+host+path],timeout=7)
  need(code==str(expected).encode(),'Reviewed route fence is not active')

def database_inventory(db):
 db.row_factory=sqlite3.Row
 need(db.execute("SELECT count(*) FROM task WHERE status IN ('running','queued','pending')").fetchone()[0]==0,'Active coding tasks')
 need(db.execute("SELECT count(*) FROM cube_recovery WHERE phase<>'complete'").fetchone()[0]==0,'Incomplete Cube recovery')
 need(db.execute("SELECT count(*) FROM cube_admission WHERE state='pending'").fetchone()[0]==0,'Ambiguous Cube allocation')
 need(db.execute("SELECT count(*) FROM runtime_migration WHERE phase NOT IN ('complete','rolled_back','aborted')").fetchone()[0]==0,'Incomplete Docker migration')
 # The wrapper does not infer a recovery archive directory from arbitrary JSON.
 # Every recorded artifact path must be explicitly present in the reviewed list.
 recovery=[json.loads(r[0]) for r in db.execute('SELECT source_artifact_paths_json FROM cube_recovery')]
 homes=[dict(r) for r in db.execute("SELECT id AS sandbox_id,container_id,workspace_mnt FROM sandbox WHERE runtime_provider='docker' ORDER BY id")]
 snapshots=[dict(r) for r in db.execute('SELECT id,image_path,status FROM snapshot ORDER BY id')]
 need(all(r['status']=='ready' for r in snapshots),'Snapshot artifact is not ready')
 migration_rows=db.execute('SELECT count(*) FROM runtime_migration').fetchone()[0]
 return {'homes':homes,'snapshots':snapshots,'recovery_paths':recovery,'migration_rows':migration_rows}

def validate_inventory(observed,expected):
 need(observed==expected,'Canonical inventory changed; refresh the reviewed plan')

def validate_sources(paths,allowed_sockets=()):
 allowed={s['path']:s for s in allowed_sockets};omitted=[]
 for name in paths:
  root=Path(name);need(root.is_absolute() and root.resolve()==root,'Source root must be canonical')
  if root.is_file():continue
  need(root.is_dir(),'Missing source root')
  for directory,dirs,files in os.walk(root,followlinks=False):
   for name in dirs+files:
    p=Path(directory)/name;s=p.lstat()
    if stat.S_ISREG(s.st_mode) or stat.S_ISDIR(s.st_mode) or stat.S_ISLNK(s.st_mode):continue
    r=allowed.get(str(p))
    need(stat.S_ISSOCK(s.st_mode) and r is not None and r['inode']==s.st_ino and r['device']==s.st_dev and isinstance(r.get('reason'),str) and bool(r['reason']),'Unreviewed special file; do not silently omit owner data')
    omitted.append(str(p))
 need(set(omitted)==set(allowed),'Reviewed ephemeral socket set changed')
 return omitted

def archive(paths,target,allowed_sockets=(),max_bytes=None):
 paths=list(dict.fromkeys(paths));need(paths,'Empty recovery role')
 need(type(max_bytes) is int and max_bytes>0,'Archive byte ceiling required')
 need(all(Path(p)!=target.parent and Path(p) not in target.parent.parents for p in paths),'Archive source contains its output directory')
 omitted=validate_sources(paths,allowed_sockets)
 listing=target.with_name(target.name+'.inputs.nul');fd=os.open(listing,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:f.write(b'\0'.join(str(Path(p)).lstrip('/').encode() for p in paths)+b'\0');f.flush();os.fsync(f.fileno())
 # GNU tar cannot archive socket inodes. Only exact, pre-reviewed stopped-source
 # sockets are omitted; regular files never match an exclusion wildcard.
 args=['tar','--create','--file=-','--directory=/','--numeric-owner','--acls','--xattrs','--sparse','--no-wildcards']
 for p in omitted:args+=['--exclude='+p.lstrip('/')]
 args+=['--null','--verbatim-files-from','--files-from='+str(listing)]
 part=target.with_name(target.name+'.INCOMPLETE');fd=os.open(part,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600);log=target.with_name(target.name+'.private.log')
 with os.fdopen(fd,'wb') as output,open(log,'xb') as errors:
  os.chmod(log,0o600);p=subprocess.run(args,stdout=output,stderr=errors,timeout=900,preexec_fn=lambda:resource.setrlimit(resource.RLIMIT_FSIZE,(max_bytes,max_bytes)),pass_fds=CAPTURE_FDS);output.flush();os.fsync(output.fileno())
 need(p.returncode==0 and log.stat().st_size==0,'Archive failed or emitted a warning; incomplete evidence retained')
 os.link(part,target);part.unlink()
 return {'bytes':target.stat().st_size,'sha256':sha(target),'ephemeral_sockets_omitted':omitted}

@contextlib.contextmanager
def operator_locks(inherited):
 fds=[]
 try:
  for i,name in enumerate(LOCKS):
   need(Path(name).parent.resolve()==Path(name).parent,'Lock ancestor must be canonical')
   if inherited:
    fd=inherited[i];a=os.fstat(fd);b=os.stat(name,follow_symlinks=False);need((a.st_dev,a.st_ino)==(b.st_dev,b.st_ino),'Inherited lock changed')
    check=os.open(name,os.O_RDWR|os.O_NOFOLLOW)
    try:
     try:fcntl.flock(check,fcntl.LOCK_EX|fcntl.LOCK_NB)
     except BlockingIOError:pass
     else:raise RuntimeError('Inherited lock is not held')
    finally:os.close(check)
   else:
    fd=os.open(name,os.O_RDWR|os.O_NOFOLLOW);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);fds.append(fd)
   s=os.fstat(fd);need(stat.S_ISREG(s.st_mode) and s.st_nlink==1 and s.st_uid==0 and not(s.st_mode&0o022),'Unsafe operator lock')
  yield tuple(inherited or fds)
 finally:
  for fd in fds:os.close(fd)

def observe(config,require_stopped):
 verify_inputs(config)
 cold_pair.no_open_users([DB,Path(str(DB)+'-wal'),Path(str(DB)+'-shm')])
 with contextlib.closing(sqlite3.connect(DB.as_uri()+'?mode=ro',uri=True)) as db:
  db.execute('BEGIN');inventory=database_inventory(db)
 validate_inventory(inventory,config['inventory'])
 cp=json.loads(run(['docker','inspect',config['controller_id']]))[0]
 need(cp['Id']==config['controller_id'] and cp['Image']==config['controller_image'] and cp['Name']=='/src-sandboxd-1','Controller identity changed')
 need(hashlib.sha256(canonical(cp['Config']['Env'])).hexdigest()==config['controller_env_sha256'],'Active controller environment changed')
 env=dict(x.split('=',1) for x in cp['Config']['Env'] if '=' in x)
 need(env.get('SANDBOXD_ENV_FILE')==str(PREVIEW_KEY),'Controller signing-key environment file is not the reviewed persistent path')
 if require_stopped:need(not cp['State']['Running'] and cp['State']['Pid']==0 and cp['HostConfig']['RestartPolicy']['Name']=='no','Controller must already be stopped with restart disabled')
 ids=[r['container_id'] for r in inventory['homes']];containers=json.loads(run(['docker','inspect',*ids])) if ids else []
 byid={x['Id']:x for x in containers};home_paths=[]
 for expected in config['docker_homes']:
  c=byid.get(expected['container_id']);need(c and c['Image']==expected['image'],'Retained Docker generation/image changed')
  mounts=[m for m in c['Mounts'] if m['Destination']=='/home/sandbox'];need(len(mounts)==1 and mounts[0]['Type']=='bind' and mounts[0]['Source']==expected['source'] and len(c['Mounts'])==1,'Retained home mapping changed or extra mounts require review')
  if require_stopped:need(not c['State']['Running'] and c['State']['Pid']==0,'A Docker data writer is still running')
  home_paths.append(expected['source'])
 need({r['container_id'] for r in config['docker_homes']}==set(ids) and len(home_paths)==len(ids),'Incomplete/duplicate Docker home inventory')
 # No unrelated running container may share a selected writable owner mount.
 allids=run(['docker','ps','-q']).decode().split()
 if allids:
  for c in json.loads(run(['docker','inspect',*allids])):
   for m in c['Mounts']:
    if m.get('RW'):
     source=Path(m['Source'])
     need(not any(source==Path(h) or source in Path(h).parents or Path(h) in source.parents for h in home_paths),'Another container has a writable owner-home mount')
 if require_stopped:
  with urllib.request.urlopen('http://127.0.0.1:2019/config/',timeout=3) as r:
   raw=r.read(2*1024*1024+1);need(len(raw)<=2*1024*1024,'Oversized Caddy configuration')
  need(hashlib.sha256(canonical(json.loads(raw))).hexdigest()==config['reviewed_caddy_sha256'],'Reviewed routing fence changed')
  route_fence(config)
  for unit in config['stopped_writer_units']:
   need(re.fullmatch(r'baarcha-[a-z0-9-]+\.(?:timer|service)',unit),'Unreviewed unit name')
   fields=dict(x.split('=',1) for x in run(['systemctl','show',unit,'--property=LoadState','--property=ActiveState']).decode().splitlines());need(fields=={'LoadState':'loaded','ActiveState':'inactive'},'Direct writer unit is not inactive')
 return inventory,home_paths

PG_DUMP_SCRIPT=r"""
import fs from 'node:fs';import {spawnSync} from 'node:child_process';
const [output,listing,errors]=process.argv.slice(1),u=new URL(process.env.DATABASE_URL);
if(!['postgres:','postgresql:'].includes(u.protocol)||u.search||u.hash)throw Error('Review database connection options before capture');
const env={PATH:'/usr/bin:/bin',PGHOST:u.hostname,PGPORT:u.port||'5432',PGDATABASE:decodeURIComponent(u.pathname.slice(1)),PGUSER:decodeURIComponent(u.username),PGPASSWORD:decodeURIComponent(u.password),PGCONNECT_TIMEOUT:'10'};
const fd=fs.openSync(output,'wx',0o600),err=fs.openSync(errors,'wx',0o600);
try{const p=spawnSync('/usr/bin/pg_dump',['--format=custom','--no-owner','--no-privileges'],{env,stdio:['ignore',fd,err],timeout:300000});if(p.status!==0)throw Error('PostgreSQL dump failed; inspect private evidence');fs.fsyncSync(fd);}finally{fs.closeSync(fd);fs.closeSync(err);}
const list=fs.openSync(listing,'wx',0o600);try{const p=spawnSync('/usr/bin/pg_restore',['--list',output],{env:{PATH:'/usr/bin:/bin'},stdio:['ignore',list,'ignore'],timeout:60000});if(p.status!==0)throw Error('PostgreSQL dump list verification failed');fs.fsyncSync(list);}finally{fs.closeSync(list);}
"""
def pg_dump(stage,max_bytes):
 target=stage/'platform-db';part=stage/'platform-db.INCOMPLETE'
 run([NODE,'--env-file=/opt/baarcha/landing.env','--input-type=module','-e',PG_DUMP_SCRIPT,str(part),str(stage/'platform-db.toc.PRIVATE'),str(stage/'platform-db.errors.PRIVATE')],timeout=370,max_bytes=max_bytes)
 need(part.stat().st_size>0,'Empty PostgreSQL dump');os.link(part,target);part.unlink();return {'bytes':target.stat().st_size,'sha256':sha(target),'dump_and_list_passed':True,'sql_restore_verified':False}

def clean_control(result):
 need(result.returncode==0 and not result.stderr.strip(),'PostgreSQL control probe failed or warned; clean shutdown is unproven')
 state=re.search(rb'^Database cluster state:\s*(.+)$',result.stdout,re.M)
 need(state and state[1].strip()==b'shut down' and b'WARNING' not in result.stdout,'Clean PostgreSQL shutdown is unproven; retain all WAL and use isolated recovery')

def pg_control(config,stage):
 p=config['postgres'];need(p['image']==PG18 and p['major']==18,'Reviewed PostgreSQL18 image required')
 home=next((r for r in config['docker_homes'] if r['sandbox_id']==p['sandbox_id']),None);need(home and home['container_id']==p['container_id'],'Postgres owner/container changed')
 data=Path(p['data']);need(data.resolve()==data and Path(home['source']) in data.parents and (data/'PG_VERSION').read_text().strip()=='18','Postgres data path changed')
 need((data.stat().st_dev,data.stat().st_ino)==(p['data_device'],p['data_inode']),'Postgres data directory identity changed')
 need(not (data/'postmaster.pid').exists(),'Postgres PID file remains; do not remove it to bypass the check')
 need(p['socket_paths'],'Exact observed PostgreSQL socket/lock paths required')
 for name in p['socket_paths']:need(not Path(name).exists() and not Path(name).is_symlink(),'Postgres socket/lock did not disappear naturally')
 name='cube-backup-pg-control-'+str(os.getpid());args=['docker','run','--rm','--name',name,'--network=none','--read-only','--user','1000:1000','--cap-drop=ALL','--security-opt=no-new-privileges:true','--cpus=1','--memory=256m','--memory-swap=256m','--pids-limit=32','--tmpfs','/var/lib/postgresql:rw,noexec,nosuid,nodev,size=1m','--mount','type=bind,src='+str(data)+',dst=/pgdata,readonly','--entrypoint','/usr/lib/postgresql/18/bin/pg_controldata',PG18,'/pgdata']
 result=subprocess.run(args,capture_output=True,timeout=45,pass_fds=CAPTURE_FDS);out=stage/'postgres-control.PRIVATE'
 fd=os.open(out,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:f.write(result.stdout+b'\nSTDERR\n'+result.stderr);f.flush();os.fsync(f.fileno())
 clean_control(result)
 need(subprocess.run(['docker','inspect',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode!=0,'Owned control probe container remains')
 return {'major':18,'state':'shut down','control_sha256':sha(out),'sql_restore_verified':False}

def remaining_budget(config,results,stage):
 used=sum(r.get('bytes',0) for r in results.values())
 left=config['role_byte_budget']-used
 need(left>0 and shutil.disk_usage(stage).free>=PAIR_RESERVE+config['role_byte_budget']+left,'Role byte budget or paired-copy space reserve exhausted')
 return left

def capture(config,output,inherited):
 global CAPTURE_FDS
 validate_config(config);cold_pair.native_host();need(DB.resolve()==DB,'Canonical database required');parent=private(Path(output).parent,True);stage=parent/Path(output).name;need(not stage.exists(),'Never overwrite an existing generation')
 with operator_locks(inherited) as outer_fds,cold_pair.lock_file(str(DB)+'.maintenance.lock',cold_pair.CONTROLLER_MARKER,True) as database_fd:
  CAPTURE_FDS=outer_fds+(database_fd,)
  inventory,homes=observe(config,True);stage.mkdir(mode=0o700);write(stage/'started.json',{'version':1,'at':time.time(),'config_sha256':hashlib.sha256(canonical(config)).hexdigest(),'full_pair_captured':False})
  try:
   image=config['image_archive'];need(sha(private(image['path']))==image['sha256'],'Immutable image archive changed')
   need({r['image'] for r in config['docker_homes']}|{config['controller_image']}<=set(image['image_ids']),'Recovery image archive misses an installed image')
   need(str(PREVIEW_KEY) in config['role_paths']['controller-config'],'Persistent Cube preview signing key is missing from recovery inputs')
   need('/opt/baarcha/landing.env' in config['role_paths']['controller-config'],'Platform connection/key references missing')
   need(config['upload_storage_review'] in ('s3-only-confirmed','disk-roots-listed'),'Upload storage disposition is unreviewed')
   if config['upload_storage_review']=='disk-roots-listed':need(config['role_paths']['library-extra'],'Disk upload roots omitted')
   need(not inventory['migration_rows'] or config['role_paths']['rollback-extra'],'Migration archives need explicit path coverage')
   for record in inventory['recovery_paths']:
    need(isinstance(record,dict) and all(any(Path(v)==Path(p) or Path(p) in Path(v).parents for p in config['role_paths']['rollback-extra']) for v in record.values()),'Recovery archive references missing from rollback role')
   need(shutil.disk_usage(stage).free>=config['minimum_free_bytes'],'Insufficient reviewed backup space')
   results={'postgres':pg_control(config,stage)}
   inspections=json.loads(run(['docker','inspect',config['controller_id'],*[r['container_id'] for r in config['docker_homes']]]))
   write(stage/'frozen-identities.PRIVATE.json',{'config':config,'containers':inspections})
   for role in ('controller-config','worker-config','worker-launch'):
    paths=config['role_paths'][role]+([image['path'],str(stage/'frozen-identities.PRIVATE.json')] if role=='controller-config' else []);results[role]=archive(paths,stage/role,max_bytes=remaining_budget(config,results,stage));observe(config,True)
   results['rollback']=archive(homes+config['role_paths']['rollback-extra'],stage/'rollback',config['ephemeral_sockets'],remaining_budget(config,results,stage));observe(config,True)
   library=list(dict.fromkeys([r['image_path'] for r in inventory['snapshots']]+config['role_paths']['library-extra']));results['library']=archive(library,stage/'library',max_bytes=remaining_budget(config,results,stage));observe(config,True)
   results['platform-db']=pg_dump(stage,remaining_budget(config,results,stage));observe(config,True)
   role_bytes=sum(r.get('bytes',0) for r in results.values())
   need(role_bytes<=config['role_byte_budget'] and shutil.disk_usage(stage).free>=PAIR_RESERVE+role_bytes,'Role budget or remaining paired-copy reserve exceeded')
   with contextlib.closing(sqlite3.connect(DB.as_uri()+'?mode=ro',uri=True)) as source,contextlib.closing(sqlite3.connect(stage/'controller-before-pause.sqlite')) as dest:source.backup(dest)
   (stage/'controller-before-pause.sqlite').chmod(0o600)
   results['controller-key']={'source':'/var/lib/sandboxd/secrets.key','sha256':sha(private('/var/lib/sandboxd/secrets.key'))}
   write(stage/'complete.json',{'version':1,'closed_at':time.time(),'roles':results,'preview_auth_sha256':sha(PREVIEW_KEY),'full_pair_captured':False,'application_restore_verified':False,'pause_receipt':'Requires later actual fresh Cube coordinator proof; not generated here'})
   fd=os.open(stage,os.O_RDONLY|os.O_DIRECTORY);os.fsync(fd);os.close(fd)
  except BaseException:
   write(stage/'INCOMPLETE.json',{'failed_at':time.time(),'accepted':False,'sources_stopped_by_this_tool':False});raise
  finally:CAPTURE_FDS=()
 print(json.dumps({'role_files_closed':True,'output':str(stage),'full_pair_captured':False,'application_restore_verified':False}))

def main():
 parser=argparse.ArgumentParser();parser.add_argument('action',choices=['plan','capture-frozen']);parser.add_argument('--config',required=True);parser.add_argument('--output');parser.add_argument('--inherited-lock-fds',default='');args=parser.parse_args();config=json.loads(private(args.config).read_text())
 validate_config(config)
 if args.action=='plan':
  print(json.dumps({'prepared_only':True,'docker_homes':len(config['docker_homes']),'snapshots':len(config['inventory']['snapshots']),'required_roles':sorted(config['role_paths']),'full_pair_captured':False}));return
 need(args.output,'New private output required');fds=[int(x) for x in args.inherited_lock_fds.split(',') if x];need(not fds or len(fds)==len(LOCKS),'Exact inherited lock set required');capture(config,args.output,fds)
if __name__=='__main__':
 try:main()
 except Exception as e:print(type(e).__name__+': '+str(e),file=sys.stderr);sys.exit(1)
