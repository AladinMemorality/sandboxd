#!/usr/bin/env python3
"""VPS-only consistent MySQL dump, isolated restore, closed-binlog HDD archive.
No source data is deleted and no production settings are changed by this tool.
"""
import fcntl,hashlib,json,os,pathlib,re,shlex,subprocess,time
os.umask(0o077)
ROOT=pathlib.Path('/var/backups/baarcha-cube-mysql')/time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())
ROOT.mkdir(parents=True,mode=0o700)
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-o','BatchMode=yes','root@127.0.0.1']
locks=[]
for path in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
 fd=os.open(path,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);locks.append(fd)
def remote(code,timeout=300):return subprocess.check_output(SSH+['python3 -'],input=code.encode(),timeout=timeout)
def save(name,obj):
 p=ROOT/name;p.write_text(json.dumps(obj,indent=2)+'\n');os.chmod(p,0o600)
# Only SQL output with names/counts is returned; credentials/dump stay private.
helper='''import os,pathlib,subprocess,json,time,secrets,shutil
os.umask(0o077)
def sql(q,container="cube-sandbox-mysql"):
 r=subprocess.run(["docker","exec","-i",container,"sh","-c",'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec /usr/bin/mysql -uroot --batch --raw --skip-column-names'],input=q,text=True,capture_output=True)
 if r.returncode:raise RuntimeError("MySQL query failed")
 return r.stdout
'''
checks="SELECT COUNT(*) FROM performance_schema.replication_connection_configuration; SELECT COUNT(*) FROM performance_schema.replication_group_members; SELECT COUNT(*) FROM information_schema.processlist WHERE COMMAND LIKE 'Binlog Dump%';"
pre=json.loads(remote(helper+f'print(json.dumps(sql({checks!r})))'))
assert pre.split()==['0','0','0'],'Replication consumers require their own retention contract'
inner='/root/baarcha-mysql-backup-'+ROOT.name
code=helper+f'''root=pathlib.Path({inner!r});root.mkdir(mode=0o700)
image=json.loads(subprocess.check_output(["docker","inspect","cube-sandbox-mysql"]))[0]["Image"]
counts=sql("SELECT TABLE_NAME,TABLE_ROWS FROM information_schema.tables WHERE TABLE_SCHEMA='cube_mvp' ORDER BY TABLE_NAME")
with (root/"all-databases.sql").open("wb") as out,(root/"dump.stderr").open("wb") as err:
 p=subprocess.run(["docker","exec","cube-sandbox-mysql","sh","-c",'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec /usr/bin/mysqldump -uroot --all-databases --single-transaction --source-data=2 --set-gtid-purged=OFF --routines --events --triggers --hex-blob'],stdout=out,stderr=err)
 if p.returncode:raise RuntimeError("consistent dump failed")
# Rotate only after the consistent dump has recorded its own recovery position.
sql("FLUSH BINARY LOGS")
logs=[r.split("\\t")[:2] for r in sql("SHOW BINARY LOGS").splitlines()]
(root/"closed.list").write_text("\\n".join(x[0] for x in logs[:-1])+"\\n")
source_env=dict(x.split("=",1) for x in json.loads(subprocess.check_output(["docker","inspect","cube-sandbox-mysql"]))[0]["Config"]["Env"])
(root/"root.env").write_text("MYSQL_ROOT_PASSWORD="+source_env["MYSQL_ROOT_PASSWORD"]+"\\n")
name="baarcha-mysql-restore-"+{ROOT.name.lower()!r}
scratch=pathlib.Path("/dev/shm")/name;scratch.mkdir(mode=0o700)
subprocess.run(["docker","run","-d","--name",name,"--label","baarcha.backup-verification="+{ROOT.name!r},"--network","none","--cpus","1","--memory","768m","--memory-swap","768m","--env-file",str(root/"root.env"),"-v",str(scratch)+":/var/lib/mysql",image,"--skip-log-bin","--innodb-buffer-pool-size=64M"],check=True,stdout=subprocess.DEVNULL)
try:
 for attempt in range(300):
  try:sql("SELECT 1",name);break
  except RuntimeError:time.sleep(1)
 else:raise RuntimeError("restore database did not become ready")
 with (root/"all-databases.sql").open("rb") as data,(root/"restore.stderr").open("wb") as err:
  p=subprocess.run(["docker","exec","-i",name,"sh","-c",'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec /usr/bin/mysql -uroot'],stdin=data,stderr=err,stdout=subprocess.DEVNULL)
  if p.returncode:raise RuntimeError("isolated restore failed")
 tables=sql("SELECT TABLE_NAME FROM information_schema.tables WHERE TABLE_SCHEMA='cube_mvp' ORDER BY TABLE_NAME",name).splitlines()
 expected=[x.split("\\t")[0] for x in counts.splitlines()]
 if tables!=expected:raise RuntimeError("restored tables differ")
 checks=sql("CHECK TABLE "+",".join("cube_mvp.`"+x+"`" for x in tables),name)
 if any(not x.endswith("\\tstatus\\tOK") for x in checks.splitlines()):raise RuntimeError("restored table validation failed")
 result={{"dump_restored":True,"tables_checked":len(tables),"closed_logs":logs[:-1],"active_log":logs[-1][0],"inner_directory":str(root),"database_image":image}}
 (root/"restore-result.json").write_text(json.dumps(result))
 print(json.dumps(result))
finally:
 info=json.loads(subprocess.check_output(["docker","inspect",name]))[0]
 if info["Config"]["Labels"].get("baarcha.backup-verification")!={ROOT.name!r}:raise RuntimeError("restore ownership changed")
 subprocess.run(["docker","rm","-f",name],check=True,stdout=subprocess.DEVNULL)
 shutil.rmtree(scratch)
'''
result=json.loads(remote(code,timeout=900));save('restore-result.json',result)
print('isolated dump restore passed',result['tables_checked'],flush=True)
for entry in result['closed_logs']:assert re.fullmatch(r'binlog\.\d{6}',entry[0])
# Write archives directly onto HDD. Pipe failure cannot produce a success receipt.
commands={
 'all-databases.sql.zst':'zstd -q -1 -T1 -c '+shlex.quote(inner+'/all-databases.sql'),
 'closed-binlogs.tar.zst':'set -o pipefail; tar -C /data/control/mysql -cf - --verbatim-files-from --files-from='+shlex.quote(inner+'/closed.list')+' | zstd -q -1 -T1 -c',
}
archive=[]
for name,command in commands.items():
 partial=ROOT/(name+'.partial')
 with partial.open('xb') as out:
  subprocess.run(SSH+['bash -c '+shlex.quote(command)],stdout=out,check=True,timeout=2400)
  out.flush();os.fsync(out.fileno())
 subprocess.run(['zstd','-q','--test',str(partial)],check=True,timeout=600)
 h=hashlib.sha256()
 with partial.open('rb') as f:
  for block in iter(lambda:f.read(4*1024*1024),b''):h.update(block)
 os.rename(partial,ROOT/name)
 archive.append({'file':name,'bytes':(ROOT/name).stat().st_size,'sha256':h.hexdigest()})
 print('archive verified',name,(ROOT/name).stat().st_size,flush=True)
# Check exact membership; zstd already verified complete stream integrity.
p=subprocess.Popen(['zstd','-q','-dc',str(ROOT/'closed-binlogs.tar.zst')],stdout=subprocess.PIPE)
r=subprocess.run(['tar','-tf','-'],stdin=p.stdout,capture_output=True,check=True);p.stdout.close();assert p.wait()==0
assert r.stdout.decode().splitlines()==[x[0] for x in result['closed_logs']]
save('verified.json',{'verified_at':time.time(),'restore':result,'archives':archive,'source_bytes':sum(int(x[1]) for x in result['closed_logs']),'production_settings_changed':False,'source_logs_deleted':False})
print('backup verified',str(ROOT),flush=True)
# Latest receipt is updated only after both archives and the restore pass.
latest=ROOT.parent/'latest.json';tmp=latest.with_suffix('.tmp')
tmp.write_text(json.dumps({'directory':str(ROOT),'verified_at':time.time(),'restore_verified':True})+'\n');os.replace(tmp,latest)
# The HDD archives are authoritative; remove only this run's private worker copy.
remote('import pathlib,shutil\np=pathlib.Path('+repr(inner)+')\nassert p.parent==pathlib.Path("/root") and p.name.startswith("baarcha-mysql-backup-") and not p.is_symlink()\nshutil.rmtree(p)\n')
# Keep at least two completed backups and fourteen days of history. Partial or
# unfamiliar directories are retained for review, never counted as a backup.
import shutil
completed=[]
for p in ROOT.parent.iterdir():
 if not p.is_dir() or p.is_symlink() or not re.fullmatch(r'\d{8}T\d{6}Z',p.name):continue
 try:r=json.loads((p/'verified.json').read_text())
 except (FileNotFoundError,ValueError):continue
 if r.get('restore',{}).get('dump_restored'):completed.append((r['verified_at'],p))
for at,p in sorted(completed)[:-2]:
 if at<time.time()-14*86400:shutil.rmtree(p)
