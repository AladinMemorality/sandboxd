"""Enroll the VPS 768 MiB template; preserve the current controller image.

The resource ceilings and all existing template contracts stay unchanged.
The controller is stopped while its immutable durable contract is extended.
"""
import contextlib,copy,hashlib,importlib.util,json,os,pathlib,signal,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008');release=root/'balanced-release-a583d45'
BASE='sha256:0d4db42569301df88087823bd40046f090c033bc564ee99c81e41e7f9650c8ab'
spec=importlib.util.spec_from_file_location('maintenance','/usr/local/libexec/baarcha-cube-maintenance.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);b=m.b
def run(args):return subprocess.check_output(args,stderr=subprocess.STDOUT,timeout=180)
def inspect():return json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
def put(p,v):b.atomic(p,b.encoded(v))
def compose(*args):return run(['docker','compose','--project-directory','/opt/sandboxd/src','-f','/opt/sandboxd/src/docker-compose.yml','-f',str(b.COMPOSE),'-f',str(b.ACTIVE),*args])
@contextlib.contextmanager
def database(write=False):
    db=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode='+('rw' if write else 'ro'),uri=True,timeout=10)
    try:yield db
    finally:db.close()
def bindings(db):return db.execute('select sandbox_id,runtime_id,template_id from runtime_binding order by sandbox_id').fetchall()
def quiet(db):
    assert db.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0,'Active task; release postponed'
    assert db.execute("select count(*) from cube_admission where state='pending'").fetchone()[0]==0,'Provider operation in progress'
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
def encoded_contract(policy):
    budget=policy['resource_budget']
    # Match the Go struct field order and sorted map keys, with a baseline
    # byte comparison below before using this encoding for the CAS update.
    v={'budget':{k:budget[k] for k in ['cpu_millis','memory_mb','runtime_slots','build_slots']},'templates':{}}
    v['budget']['profiles']={k:{f:budget['profiles'][k][f] for f in ['cpu_millis','writable_disk_mb','kind']} for k in sorted(budget['profiles'])}
    v['templates']={k:{f:policy['templates'][k][f] for f in ['cpu_count','memory_mb']} for k in sorted(policy['templates'])}
    return json.dumps(v,separators=(',',':'))
with b.locked():
    release.mkdir(mode=0o700,exist_ok=False)
    receipt=json.loads((root/'balanced-template-a583d45.json').read_text());assert receipt['ready'] and receipt['worker']=='vps' and receipt['memory_mb']==768
    template=receipt['template_id'];before=inspect();assert before['Image']==BASE
    env=dict(x.split('=',1) for x in before['Config']['Env']);assert int(env.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
    old_policy=json.loads(env['SANDBOXD_CUBE_ADMISSION']);policy=copy.deepcopy(old_policy)
    assert template not in policy['templates']
    standard='tpl-78e4edb3d629465e9d8372c1'
    policy['templates'][template]=copy.deepcopy(policy['templates'][standard])
    policy['resource_budget']['profiles'][template]=copy.deepcopy(policy['resource_budget']['profiles'][standard])
    policy['templates'][template]['memory_mb']=768
    assert policy['templates'][template]=={'cpu_count':1,'memory_mb':768}
    assert policy['resource_budget']['profiles'][template]=={'cpu_millis':100,'writable_disk_mb':4096,'kind':'runtime'}
    fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);vps=next(w for w in fleet['workers'] if w['id']=='vps');assert vps['admission']==old_policy
    assert all(w['draining'] for w in fleet['workers'] if w['id']!='vps');vps['admission']=policy
    desired={'SANDBOXD_CUBE_ADMISSION':json.dumps(policy,separators=(',',':')),'SANDBOXD_CUBE_FLEET':json.dumps(fleet,separators=(',',':'))}
    originals={str(p):p.read_bytes() for p in [b.COMPOSE,b.ACTIVE,b.STOP]}
    stop=json.loads(originals[str(b.STOP)]);assert stop['controller_id']==before['Id'] and stop['admission']==old_policy
    for p in [b.COMPOSE,b.ACTIVE,b.STOP]:b.atomic(release/(p.name+'.before'),originals[str(p)])
    with database() as db:
        quiet(db);baseline=bindings(db);old_contract=db.execute("select contract from cube_resource_budget where worker_id='vps'").fetchone()[0]
        assert old_contract==encoded_contract(old_policy)
        with sqlite3.connect(release/'before.PRIVATE.sqlite') as backup:db.backup(backup)
    new_contract=encoded_contract(policy);put(release/'new-policy.json',policy)
    old_render=json.loads(compose('config','--format','json'));candidate=before['Image']
    online=b.strict(b.http('/config/',2019));scope=json.loads((root/'vps-resize-plan.PRIVATE.json').read_text())['routing'];scope['online_sha256']=hashlib.sha256(json.dumps(online,sort_keys=True,separators=(',',':')).encode()).hexdigest();routes=m.routing_variants(online,scope)
    put(release/'routing-before.PRIVATE.json',online)
    helpers={}
    for path in helpers:subprocess.run(['cp','-p',str(path),str(release/(path.name+'.before'))],check=True)
    timers={name:run(['systemctl','show',name,'-p','ActiveState','--value']).decode().strip() for name in b.TIMERS};put(release/'timers-before.json',timers)
    stopped=False;contract_changed=False
    def interrupted(*args):raise SystemExit('release interrupted')
    signal.signal(signal.SIGTERM,interrupted)
    try:
        route(routes['offline']);run(['systemctl','stop',*b.TIMERS]);time.sleep(3)
        with database() as db:quiet(db);assert bindings(db)==baseline
        stopped=True
        run(['docker','update','--restart=no',before['Id']]);run(['docker','stop','--time=-1',before['Id']])
        stopped_state=inspect()['State'];assert not stopped_state['Running'] and stopped_state['ExitCode']==0
        with database(True) as db:
            quiet(db);assert bindings(db)==baseline
            assert db.execute("update cube_resource_budget set contract=? where worker_id='vps' and contract=?",(new_contract,old_contract)).rowcount==1
            db.commit();contract_changed=True
        for path in [b.COMPOSE,b.ACTIVE]:
            value=json.loads(originals[str(path)]);service=value['services']['sandboxd'];service['image']=candidate;service.setdefault('environment',{}).update(desired);put(path,value)
        expected=copy.deepcopy(old_render);expected['services']['sandboxd']['image']=candidate;expected['services']['sandboxd']['environment'].update(desired)
        assert json.loads(compose('config','--format','json'))==expected
        compose('up','-d','--no-deps','--no-build','--pull','never','sandboxd')
        compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy')
        ready();after=inspect();assert after['Image']==candidate
        for destination,source in helpers.items():run(['install','-m','755',str(source),str(destination)])
        stop['controller_id']=after['Id'];stop['admission']=policy;put(b.STOP,stop)
        with database() as db:assert bindings(db)==baseline
        assert json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe']))['consistent']
        for name,state in timers.items():
            if state=='active':run(['systemctl','start',name])
        route(online)
        result={'deployed':True,'revision':'a583d45','image':candidate,'template_id':template,'resource_ceilings_unchanged':True,'bindings_preserved':len(baseline),'coding_queue_enabled':False,'b200_contacted':False}
        put(release/'deployed.json',result);print(json.dumps(result),flush=True)
    except BaseException:
        if stopped:
            current=inspect()
            if current['State']['Running']:run(['docker','stop','--time=-1',current['Id']])
        if contract_changed:
            with database(True) as db:
                assert db.execute('select count(*) from cube_admission where worker_id=? and template_id=?',('vps',template)).fetchone()[0]==0
                assert db.execute("update cube_resource_budget set contract=? where worker_id='vps' and contract=?",(old_contract,new_contract)).rowcount==1;db.commit()
        for destination in helpers:run(['install','-m','755',str(release/(destination.name+'.before')),str(destination)])
        for path in [b.COMPOSE,b.ACTIVE]:b.atomic(path,originals[str(path)])
        if stopped:
            compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','sandboxd')
            compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy');ready()
        old_stop=json.loads(originals[str(b.STOP)]);old_stop['controller_id']=inspect()['Id'];put(b.STOP,old_stop)
        for name,state in timers.items():
            if state=='active':run(['systemctl','start',name])
        route(online)
        raise
