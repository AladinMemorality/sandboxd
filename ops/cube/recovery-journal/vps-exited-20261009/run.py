"""Reconcile the one terminated guest-panic resume without changing controller code.

No B200 worker request is made. The controller is drained and stopped before the
guarded offline CLI verifies the known paused runtime and releases its lease.
"""
import contextlib,copy,hashlib,importlib.util,json,os,pathlib,shlex,signal,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008');release=root/'exited-recovery-01';artifacts=root/'resume-retry-release-d5b07bb'
BASE='sha256:f3194ece7c0b70101ab0191e4fdfab73dc1c7adf1365765f67887a3e9f096ab6'
KEY='app:01M24GEQ88RFYJBJ86KYW5BVN6';RUNTIME='38a1bd34cfa54a9aa47b4b121d035e46'
spec=importlib.util.spec_from_file_location('maintenance','/usr/local/libexec/baarcha-cube-maintenance.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);b=m.b
TIMERS=(*b.TIMERS,'baarcha-cube-consumption.timer')
def run(args):return subprocess.check_output(args,stderr=subprocess.STDOUT,timeout=240)
def inspect():return json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
def put(p,v):b.atomic(p,b.encoded(v))
def compose(*args):return run(['docker','compose','--project-directory','/opt/sandboxd/src','-f','/opt/sandboxd/src/docker-compose.yml','-f',str(b.COMPOSE),'-f',str(b.ACTIVE),*args])
@contextlib.contextmanager
def database():
    db=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=10)
    try:yield db
    finally:db.close()
def bindings(db):return db.execute('select sandbox_id,runtime_id,template_id from runtime_binding order by sandbox_id').fetchall()
def quiet(db,pending):
    assert db.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0,'Active task; release postponed'
    actual=db.execute("select admission_key,runtime_id,worker_id from cube_admission where state='pending'").fetchall()
    assert actual==([(KEY,RUNTIME,'vps')] if pending else []),'Unexpected provider operation'
    assert db.execute("select count(*) from cube_relocation where phase='fenced'").fetchone()[0]==0,'Unfinished relocation'
def route(v):
    req=urllib.request.Request('http://127.0.0.1:2019/load',data=json.dumps(v).encode(),headers={'Content-Type':'application/json'},method='POST')
    with urllib.request.urlopen(req,timeout=10) as response:assert response.status==200
    assert b.strict(b.http('/config/',2019))==v
def ready():
    deadline=time.monotonic()+150
    while time.monotonic()<deadline:
        try:
            if b.http('/readyz',9090).strip()==b'ready':return
        except Exception:pass
        time.sleep(1)
    raise RuntimeError('controller readiness failed')
def provider_drained():
    # Shared metadata is read locally through the VPS worker; no B200 RPC.
    code='import json,subprocess\ntables='+repr(sorted(m.TABLES))+'\n'+'''command=['docker','exec','-i','cube-sandbox-mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names']
sql='START TRANSACTION READ ONLY;'+''.join("SELECT '"+t+"',status,count(*) FROM cube_mvp."+t+" GROUP BY status;" for t in tables)+'COMMIT;'
p=subprocess.run(command,input=sql,text=True,capture_output=True,timeout=20);assert p.returncode==0
out={t:{} for t in tables}
for line in p.stdout.splitlines():
 t,status,n=line.split('\\t');assert t in out and status not in out[t];out[t][status]=int(n)
print(json.dumps(out))
'''
    ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
    result=json.loads(run(ssh+['python3 -c '+shlex.quote(code)]))
    for table,statuses in result.items():
        assert set(statuses)<=m.TERMINAL|({'PARTIALLY_READY'} if table=='t_cube_template_definition' else set()),'Provider job in progress'
    put(release/'provider-terminal-counts.json',result)

with b.locked():
    before=inspect();assert before['Image']==BASE
    assert not (release/'maintenance-entered.json').exists(),'Reconcile existing maintenance before rerunning'
    env=dict(x.split('=',1) for x in before['Config']['Env']);assert int(env.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
    fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);assert all(w['draining'] for w in fleet['workers'] if w['id']!='vps')
    with database() as db:
        quiet(db,False);baseline=bindings(db)
        assert db.execute("select count(*) from cube_recovery where phase<>'complete'").fetchone()[0]==0
        assert db.execute('select runtime_id,state,charged from cube_admission where admission_key=?',(KEY,)).fetchone()==(RUNTIME,'active',1)
    originals={str(p):p.read_bytes() for p in [b.COMPOSE,b.ACTIVE,b.STOP]}
    stop=json.loads(originals[str(b.STOP)]);assert stop['controller_id']==before['Id']
    for p in [b.COMPOSE,b.ACTIVE,b.STOP]:b.atomic(release/(p.name+'.before'),originals[str(p)])
    online=b.strict(b.http('/config/',2019));scope=json.loads((root/'vps-resize-plan.PRIVATE.json').read_text())['routing'];scope['online_sha256']=hashlib.sha256(json.dumps(online,sort_keys=True,separators=(',',':')).encode()).hexdigest();routes=m.routing_variants(online,scope)
    put(release/'routing-before.PRIVATE.json',online)
    timers={name:run(['systemctl','show',name,'-p','ActiveState','--value']).decode().strip() for name in TIMERS};put(release/'timers-before.json',timers)
    ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
    worker_lock=subprocess.Popen(ssh+["flock -n /run/lock/cube-operator-acceptance.lock sh -c 'echo LOCKED; cat >/dev/null'"],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    import select
    assert select.select([worker_lock.stdout],[],[],15)[0] and worker_lock.stdout.readline().strip()==b'LOCKED','Worker operator lock unavailable'
    stopped=False
    try:
        route(routes['offline']);run(['systemctl','stop',*TIMERS]);run(['systemctl','stop','baarcha-cube-consumption.service']);time.sleep(3)
        with database() as db:quiet(db,False);assert bindings(db)==baseline
        put(release/'maintenance-entered.json',{'controller_id':before['Id'],'at':time.time()})
        run(['docker','update','--restart=no',before['Id']]);run(['docker','stop','--time=-1',before['Id']]);stopped=True
        state=inspect()['State'];assert not state['Running'] and state['ExitCode']==0
        provider_drained()
        # Under both operator fences, recheck current failed identity, absence
        # of execution and unchanged current disk. A stale pause is never used.
        req=urllib.request.Request('http://10.254.240.1:18089/cube/sandbox/info?sandbox_id='+RUNTIME+'&instance_type=cubebox',headers={'X-Caller':'baarcha-controller'})
        native=json.load(urllib.request.urlopen(req,timeout=10))['data'];assert len(native)==1
        assert native[0]['sandbox_id']==RUNTIME and native[0]['host_id']=='10.0.2.15' and native[0]['template_id']=='tpl-3c4e83ebf6c641f293c0e816' and native[0]['status']==2
        captured=json.loads((release/'native-backup.json').read_text())
        code="""import pathlib,json,hashlib,subprocess
v=CAPTURED;p=pathlib.Path(v['source']);st=p.stat();assert [st.st_dev,st.st_ino,st.st_size,st.st_mtime_ns]==v['source_identity']
for proc in pathlib.Path('/proc').glob('[0-9]*'):
 for fd in (proc/'fd').glob('*'):
  try:s=fd.stat()
  except OSError:continue
  assert (s.st_dev,s.st_ino)!=(st.st_dev,st.st_ino),'source still open'
assert '38a1bd34cfa54a9aa47b4b121d035e46' not in subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list']).decode()
for path in [p,pathlib.Path(v['backup'])]:
 with path.open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==v['sha256']
print('source and retained clone unchanged; no execution')
""".replace('CAPTURED',repr(captured))
        run(ssh+['python3 -c '+shlex.quote(code)])
        with database() as db:
            quiet(db,False);assert bindings(db)==baseline
            with contextlib.closing(sqlite3.connect(release/'controller-before.db')) as backup:db.backup(backup);assert backup.execute('pragma integrity_check').fetchone()[0]=='ok'
        put(release/'fence.json',{'OldRuntimeID':RUNTIME,'OldExecutionStopped':True,'ProviderRequestsDrained':True,'Expires':int(time.time())+1200,'controller_stopped':True,'native_status':2,'source_sha256':captured['sha256']})
        # stdout contains stage names only. Any detailed failures remain private.
        with (release/'operator.PRIVATE.log').open('ab') as log:
            result=subprocess.run([str(release/'incident-exited'),'run'],stdout=log,stderr=subprocess.STDOUT,timeout=1250)
        assert result.returncode==0,'Recovery stopped; private journal retained'
        assert json.loads((release/'complete.json').read_text())['restored']
    finally:
        worker_lock.stdin.close();worker_lock.wait(timeout=15)
        with database() as db:open_recoveries=db.execute("select count(*) from cube_recovery where phase<>'complete'").fetchone()[0]
        if open_recoveries:
            put(release/'maintenance-retained.json',{'incomplete_recoveries':open_recoveries,'controller_stopped':stopped})
            raise RuntimeError('Recovery journal incomplete; retain exclusive controller maintenance')
        if stopped:
            run(['docker','start',before['Id']]);run(['docker','update','--restart=unless-stopped',before['Id']])
            compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy');ready()
        for name,state in timers.items():
            if state=='active':run(['systemctl','start',name])
        route(online)
        put(release/'maintenance-exited.json',{'controller_ready':True,'at':time.time()})
print('Recovered source and restored production controller',flush=True)
