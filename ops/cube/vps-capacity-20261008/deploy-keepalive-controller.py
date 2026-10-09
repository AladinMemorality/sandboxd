"""Deploy a reviewed controller image without changing policy or bindings."""
import contextlib,copy,hashlib,importlib.util,json,os,pathlib,shlex,signal,sqlite3,subprocess,sys,re,time,urllib.request
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
revision=sys.argv[1];assert re.fullmatch('[a-f0-9]{7,40}',revision)
release=root/('resume-retry-release-'+revision)
BASE='sha256:a7fd956cacc7602449226eb164ea48c66c5f99fe119ee8ffd4c630db128ae7cf'
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
def quiet(db):
    assert db.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0,'Active task; release postponed'
    actual=db.execute("select admission_key,runtime_id,worker_id from cube_admission where state='pending'").fetchall()
    assert actual==[],'Unexpected provider operation'
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
with b.locked():
    assert not (release/'deployed.json').exists()
    before=inspect();assert before['Image']==BASE
    env=dict(x.split('=',1) for x in before['Config']['Env']);assert int(env.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
    fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);assert all(w['draining'] for w in fleet['workers'] if w['id']!='vps')
    originals={str(p):p.read_bytes() for p in [b.COMPOSE,b.ACTIVE,b.STOP]}
    stop=json.loads(originals[str(b.STOP)]);assert stop['controller_id']==before['Id']
    for p in [b.COMPOSE,b.ACTIVE,b.STOP]:b.atomic(release/(p.name+'.before'),originals[str(p)])
    with database() as db:
        quiet(db);baseline=bindings(db)
        with sqlite3.connect(release/'before.PRIVATE.sqlite') as backup:db.backup(backup)
    old_render=json.loads(compose('config','--format','json'));candidate=(release/'image.id').read_text().strip();assert candidate.startswith('sha256:') and candidate!=BASE
    online=b.strict(b.http('/config/',2019));scope=json.loads((root/'vps-resize-plan.PRIVATE.json').read_text())['routing'];scope['online_sha256']=hashlib.sha256(json.dumps(online,sort_keys=True,separators=(',',':')).encode()).hexdigest();routes=m.routing_variants(online,scope)
    put(release/'routing-before.PRIVATE.json',online)
    timers={name:run(['systemctl','show',name,'-p','ActiveState','--value']).decode().strip() for name in b.TIMERS};put(release/'timers-before.json',timers)
    stopped=False
    def interrupted(*args):raise SystemExit('release interrupted')
    signal.signal(signal.SIGTERM,interrupted)
    try:
        route(routes['offline']);run(['systemctl','stop',*b.TIMERS]);time.sleep(3)
        with database() as db:quiet(db);assert bindings(db)==baseline
        stopped=True
        run(['docker','update','--restart=no',before['Id']]);run(['docker','stop','--time=-1',before['Id']])
        state=inspect()['State'];assert not state['Running'] and state['ExitCode']==0
        for path in [b.COMPOSE,b.ACTIVE]:
            value=json.loads(originals[str(path)]);value['services']['sandboxd']['image']=candidate;put(path,value)
        expected=copy.deepcopy(old_render);expected['services']['sandboxd']['image']=candidate
        assert json.loads(compose('config','--format','json'))==expected
        compose('up','-d','--no-deps','--no-build','--pull','never','sandboxd')
        compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy')
        ready();after=inspect();assert after['Image']==candidate
        stop['controller_id']=after['Id'];put(b.STOP,stop)
        with database() as db:quiet(db);assert bindings(db)==baseline
        assert json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe']))['consistent']
        for name,state in timers.items():
            if state=='active':run(['systemctl','start',name])
        route(online)
        result={'deployed':True,'revision':revision,'image':candidate,'bindings_preserved':len(baseline),'bounded_keepalive_route':True,'coding_queue_enabled':False,'b200_contacted':False}
        put(release/'deployed.json',result);print(json.dumps(result),flush=True)
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
        put(release/'rolled-back.json',{'rolled_back':True})
        raise
