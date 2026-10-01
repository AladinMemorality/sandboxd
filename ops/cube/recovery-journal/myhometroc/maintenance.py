import pathlib,sqlite3,subprocess,json,os,sys,time,shutil
os.umask(0o077)
r=pathlib.Path('/opt/baarcha-bench/myhometroc-recovery-20260929');name='src-sandboxd-1'
expected='7470f3a8c25f890ca83635c6bed07740c2e0f622f4e5a4be6b821a6434a526f7'
def inspect():
 x=json.loads(subprocess.check_output(['docker','inspect',name]))[0];assert x['Id']==expected;return x
def db():return sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)
x=inspect()
if sys.argv[1]=='enter':
 assert x['State']['Running'] and x['HostConfig']['RestartPolicy']['Name']=='unless-stopped'
 c=db();assert c.execute("SELECT count(*) FROM task WHERE status='running'").fetchone()[0]==0
 assert c.execute("SELECT count(*) FROM cube_recovery WHERE phase<>'complete'").fetchone()[0]==0
 assert c.execute("SELECT count(*) FROM cube_admission WHERE state='pending' AND operation='create'").fetchone()[0]==0;c.close()
 assert not (r/'controller-before.db').exists()
 (r/'maintenance-entered.json').write_text(json.dumps({'container_id':expected,'at':time.time()}))
 subprocess.run(['docker','update','--restart=no',name],check=True,capture_output=True)
 subprocess.run(['docker','stop','--time','45',name],check=True,capture_output=True,timeout=60)
 assert not inspect()['State']['Running']
 c=db();assert c.execute("SELECT count(*) FROM task WHERE status='running'").fetchone()[0]==0
 dest=sqlite3.connect(r/'controller-before.db');c.backup(dest);assert dest.execute('PRAGMA integrity_check').fetchone()[0]=='ok';dest.close();c.close()
 shutil.copyfile('/var/lib/sandboxd/secrets.key',r/'controller-key.PRIVATE')
 (r/'fence.json').write_text(json.dumps({'OldRuntimeID':'4fb923eade4140bb85159fa3038efd1a','OldExecutionStopped':True,'ProviderRequestsDrained':True,'Expires':int(time.time())+900,'controller_stopped':True,'native_source_rechecked_under_worker_lock_by_operator':True}))
 print('controller stopped; consistent database and key backup retained')
elif sys.argv[1]=='exit':
 if not (r/'maintenance-entered.json').exists():print('controller maintenance was not entered');sys.exit(0)
 c=db();n=c.execute("SELECT count(*) FROM cube_recovery WHERE phase<>'complete'").fetchone()[0];c.close()
 if n:print('controller remains fenced: incomplete recovery journal');sys.exit(1)
 subprocess.run(['docker','start',name],check=True,capture_output=True)
 subprocess.run(['docker','update','--restart=unless-stopped',name],check=True,capture_output=True)
 print('controller restarted with original container identity and restart policy')
else:raise ValueError('enter or exit required')
