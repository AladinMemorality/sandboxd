"""Drain VPS mutations and replace only the tested Cubelet process."""
import contextlib,copy,hashlib,importlib.util,json,os,pathlib,shlex,signal,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008');artifacts=root/'full-pause-release-20261009';artifacts.mkdir(mode=0o700,exist_ok=True);release=artifacts/'deploy-02';release.mkdir(mode=0o700)
BASE='sha256:028b53215b95194140bfbb0356e1d6e1e1cee47f8b42a2fe5e21f7d1966e70aa'
KEY='app:01M1HH5DJRG3C8HDJ2553XR2S1';RUNTIME='63dbec13950f4568b45731328de593f7'
spec=importlib.util.spec_from_file_location('maintenance','/usr/local/libexec/baarcha-cube-maintenance.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);b=m.b
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
    env=dict(x.split('=',1) for x in before['Config']['Env']);assert int(env.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
    fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);assert all(w['draining'] for w in fleet['workers'] if w['id']!='vps')
    with database() as db:
        quiet(db,False);baseline=bindings(db)
        with contextlib.closing(sqlite3.connect(release/'before.PRIVATE.sqlite')) as backup:db.backup(backup)
    assert not (root/'restore-barrier.json').exists()
    assert json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe']))['consistent']
    online=b.strict(b.http('/config/',2019));scope=json.loads((root/'vps-resize-plan.PRIVATE.json').read_text())['routing'];scope['online_sha256']=hashlib.sha256(json.dumps(online,sort_keys=True,separators=(',',':')).encode()).hexdigest();routes=m.routing_variants(online,scope)
    put(release/'routing-before.PRIVATE.json',online)
    timer_names=[*b.TIMERS,'baarcha-cube-consumption.timer'];timers={n:run(['systemctl','show',n,'-p','ActiveState','--value']).decode().strip() for n in timer_names};put(release/'timers-before.json',timers)
    stopped=False;native_good=False
    try:
        route(routes['offline']);run(['systemctl','stop',*timer_names]);run(['systemctl','stop','baarcha-cube-consumption.service']);time.sleep(3)
        with database() as db:quiet(db,False);assert bindings(db)==baseline
        put(release/'maintenance-entered.json',{'at':time.time(),'controller_id':before['Id']})
        stopped=True;run(['docker','update','--restart=no',before['Id']]);run(['docker','stop','--time=-1',before['Id']])
        state=inspect()['State'];assert not state['Running'] and state['ExitCode']==0
        provider_drained()
        ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
        p=subprocess.run(ssh+['python3 /root/vps-full-pause-20261009-02/install-worker.py'],capture_output=True,timeout=240)
        b.atomic(release/'install.PRIVATE.log',p.stdout+p.stderr)
        assert p.returncode==0,'Native rollout requires reconciliation; private diagnostics retained'
        native=json.loads(p.stdout);assert native['deployed'];put(release/'native.json',native)
        native_good=True
    finally:
        if stopped:
            run(['docker','start',before['Id']]);run(['docker','update','--restart=unless-stopped',before['Id']])
            compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy')
            ready()
        with database() as db:quiet(db,False);assert bindings(db)==baseline
        observation=json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe']));assert observation['consistent'];put(release/'observation.json',observation)
        for name,state in timers.items():
            if state=='active':run(['systemctl','start',name])
        route(online);put(release/'maintenance-exited.json',{'controller_ready':True,'at':time.time()})
    assert native_good
    result={'deployed':True,'cubelet_sha256':native['sha256'],'bindings_preserved':len(baseline),'guest_processes_preserved':native['guest_processes_preserved'],'full_pause_snapshot_policy':True,'controller_image':BASE,'b200_contacted':False,'model_calls':False,'at':time.time()}
    put(artifacts/'deployed.json',result);print(json.dumps(result),flush=True)
