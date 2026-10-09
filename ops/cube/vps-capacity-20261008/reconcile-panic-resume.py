"""Reconcile the one terminated guest-panic resume without changing controller code.

No B200 worker request is made. The controller is drained and stopped before the
guarded offline CLI verifies the known paused runtime and releases its lease.
"""
import contextlib,copy,hashlib,importlib.util,json,os,pathlib,shlex,signal,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008');release=root/'snapshot-panic-reconcile-01';artifacts=root/'resume-retry-release-d5b07bb'
BASE='sha256:f3194ece7c0b70101ab0191e4fdfab73dc1c7adf1365765f67887a3e9f096ab6'
KEY='app:01M24DHKQ6XCASNXAYM8BH8CFY';RUNTIME='012a1853725144dda18417c4b355c78d'
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
    release.mkdir(mode=0o700)
    before=inspect();assert before['Image']==BASE
    env=dict(x.split('=',1) for x in before['Config']['Env']);assert int(env.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
    fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);assert all(w['draining'] for w in fleet['workers'] if w['id']!='vps')
    failed=root/'real-preview-density-50-balanced-02'
    assert json.loads((failed/'01M24DHKQQWT4APTJTBMTT40PG-start-error.PRIVATE.json').read_text())['status']==409
    assert time.time()-json.loads((failed/'failed.json').read_text())['at']>180
    originals={str(p):p.read_bytes() for p in [b.COMPOSE,b.ACTIVE,b.STOP]}
    stop=json.loads(originals[str(b.STOP)]);assert stop['controller_id']==before['Id']
    for p in [b.COMPOSE,b.ACTIVE,b.STOP]:b.atomic(release/(p.name+'.before'),originals[str(p)])
    with database() as db:
        quiet(db,True);baseline=bindings(db)
        with contextlib.closing(sqlite3.connect(release/'before.PRIVATE.sqlite')) as backup:db.backup(backup)
    old_render=json.loads(compose('config','--format','json'));candidate=BASE
    online=b.strict(b.http('/config/',2019));scope=json.loads((root/'vps-resize-plan.PRIVATE.json').read_text())['routing'];scope['online_sha256']=hashlib.sha256(json.dumps(online,sort_keys=True,separators=(',',':')).encode()).hexdigest();routes=m.routing_variants(online,scope)
    put(release/'routing-before.PRIVATE.json',online)
    timers={name:run(['systemctl','show',name,'-p','ActiveState','--value']).decode().strip() for name in TIMERS};put(release/'timers-before.json',timers)
    stopped=False;reconciled=False
    def interrupted(*args):raise SystemExit('release interrupted')
    signal.signal(signal.SIGTERM,interrupted)
    try:
        route(routes['offline']);run(['systemctl','stop',*TIMERS]);run(['systemctl','stop','baarcha-cube-consumption.service']);time.sleep(3)
        with database() as db:quiet(db,True);assert bindings(db)==baseline
        stopped=True
        run(['docker','update','--restart=no',before['Id']]);run(['docker','stop','--time=-1',before['Id']])
        state=inspect()['State'];assert not state['Running'] and state['ExitCode']==0
        provider_drained()
        offline={**os.environ,**env,'SANDBOXD_CUBE_API_URL':'http://127.0.0.1:20300','SANDBOXD_CUBE_MASTER_URL':'http://10.254.240.1:18089'}
        fleet['master_url']='http://10.254.240.1:18089';offline['SANDBOXD_CUBE_FLEET']=json.dumps(fleet,separators=(',',':'))
        args=[str(artifacts/'cube-migrate'),'--database','/var/lib/sandboxd/state/sandboxd.db','--migrations',str(artifacts/'migrations'),'--admission-key',KEY,'--provider-requests-drained','admission-reconcile']
        result=subprocess.run(args,env=offline,capture_output=True,timeout=90)
        b.atomic(release/'reconcile.PRIVATE.stdout',result.stdout);b.atomic(release/'reconcile.PRIVATE.stderr',result.stderr)
        assert result.returncode==0,'Offline reconciliation refused; private diagnostic retained'
        with database() as db:
            quiet(db,False);assert bindings(db)==baseline
            assert db.execute('select state,charged from cube_admission where admission_key=?',(KEY,)).fetchone()==('released',0)
        reconciled=True
        for path in [b.COMPOSE,b.ACTIVE]:
            value=json.loads(originals[str(path)]);value['services']['sandboxd']['image']=candidate;put(path,value)
        expected=copy.deepcopy(old_render);expected['services']['sandboxd']['image']=candidate
        assert json.loads(compose('config','--format','json'))==expected
        compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','sandboxd')
        compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy')
        ready();after=inspect();assert after['Image']==candidate
        stop['controller_id']=after['Id'];put(b.STOP,stop)
        with database() as db:quiet(db,False);assert bindings(db)==baseline
        assert json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe']))['consistent']
        for name,state in timers.items():
            if state=='active':run(['systemctl','start',name])
        route(online)
        result={'deployed':True,'revision':'d5b07bb','image':candidate,'bindings_preserved':len(baseline),'terminated_panic_resume_reconciled':reconciled,'coding_queue_enabled':False,'b200_contacted':False}
        put(release/'complete.json',result);print(json.dumps(result),flush=True)
    except BaseException:
        if stopped:
            current=inspect()
            if current['State']['Running']:run(['docker','stop','--time=-1',current['Id']])
        for path in [b.COMPOSE,b.ACTIVE]:b.atomic(path,originals[str(path)])
        if stopped:
            compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','sandboxd')
            compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy');ready()
        old_stop=json.loads(originals[str(b.STOP)]);old_stop['controller_id']=inspect()['Id'];put(b.STOP,old_stop)
        for name,state in timers.items():
            if state=='active':run(['systemctl','start',name])
        route(online)
        put(release/'rolled-back.json',{'rolled_back':True,'terminated_panic_resume_reconciled':reconciled})
        raise
