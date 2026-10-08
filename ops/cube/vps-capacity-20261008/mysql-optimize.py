#!/usr/bin/env python3
"""Apply bounded MySQL logging only after an independently restored HDD backup."""
import fcntl,hashlib,json,os,pathlib,re,subprocess,time
os.umask(0o077)
ROOT=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
BACKUP=pathlib.Path('/var/backups/baarcha-cube-mysql/20261008T182446Z')
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-o','BatchMode=yes','root@127.0.0.1']
locks=[]
for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
 fd=os.open(name,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);locks.append(fd)
v=json.loads((BACKUP/'verified.json').read_text())
assert 0<time.time()-v['verified_at']<3*3600 and v['restore']['dump_restored'] and v['restore']['tables_checked']==36
for a in v['archives']:
 p=BACKUP/a['file'];assert p.parent==BACKUP and p.stat().st_size==a['bytes']
 h=hashlib.sha256()
 with p.open('rb') as f:
  for b in iter(lambda:f.read(4*1024*1024),b''):h.update(b)
 assert h.hexdigest()==a['sha256']
cutoff=v['restore']['active_log'];assert re.fullmatch(r'binlog\.\d{6}',cutoff)
intent=ROOT/'mysql-optimization-intent.json'
with intent.open('x') as f:json.dump({'backup':str(BACKUP),'cutoff':cutoff,'at':time.time(),'retention_seconds':604800,'row_image':'MINIMAL'},f);f.flush();os.fsync(f.fileno())
code='''import json,subprocess,os
sqlcmd=['docker','exec','-i','cube-sandbox-mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec /usr/bin/mysql -uroot --batch --raw --skip-column-names']
def sql(q):
 r=subprocess.run(sqlcmd,input=q,text=True,capture_output=True)
 if r.returncode:raise RuntimeError('MySQL operation failed')
 return r.stdout
checks="SELECT COUNT(*) FROM performance_schema.replication_connection_configuration; SELECT COUNT(*) FROM performance_schema.replication_group_members; SELECT COUNT(*) FROM information_schema.processlist WHERE COMMAND LIKE 'Binlog Dump%';"
assert sql(checks).split()==['0','0','0'],'replication topology changed'
before=sql("SHOW BINARY LOGS")
settings=sql("SELECT @@global.binlog_row_image,@@global.binlog_expire_logs_seconds")
assert settings.strip()=='FULL\\t2592000','logging contract changed'
s=os.statvfs('/data');free=s.f_bavail*s.f_frsize
sql("SET PERSIST binlog_row_image='MINIMAL'; SET PERSIST binlog_expire_logs_seconds=604800")
'''+f'sql("PURGE BINARY LOGS TO \'{cutoff}\'")\n'+'''
after=sql("SHOW BINARY LOGS")
settings_after=sql("SELECT @@global.binlog_row_image,@@global.binlog_expire_logs_seconds")
s=os.statvfs('/data')
print(json.dumps({'before':before,'after':after,'settings_before':settings,'settings_after':settings_after,'guest_free_before':free,'guest_free_after':s.f_bavail*s.f_frsize,'existing_sessions_need_reconnect':True}))
'''
r=json.loads(subprocess.check_output(SSH+['python3 -'],input=code.encode(),timeout=120))
(ROOT/'mysql-optimization-complete.json').write_text(json.dumps(r,indent=2)+'\n')
print(json.dumps({'freed_gib':(r['guest_free_after']-r['guest_free_before'])/1024**3,'settings':r['settings_after'].strip(),'backup':str(BACKUP)}))
