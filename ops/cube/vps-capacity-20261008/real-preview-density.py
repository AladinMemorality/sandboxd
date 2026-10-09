"""Measure 50 live VPS previews without submitting tasks or executing browser JS.

Only wake reviewed Vite apps that were stopped before the test. Preserve existing
running apps, stop the test on user task activity, and restore initial state.
"""
import concurrent.futures,contextlib,importlib.util,json,os,pathlib,signal,sqlite3,statistics,subprocess,sys,time,textwrap,urllib.request,zipfile
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','-oConnectTimeout=5','root@127.0.0.1']
G=1024**3;run_dir=root/'real-preview-density-50-balanced';barrier=root/'restore-barrier.json'
def rows(query,args=()):
    with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
        db.row_factory=sqlite3.Row;return [dict(x) for x in db.execute(query,args)]
def task_ids(sid):return [r['task_id'] for r in rows('select task_id from task where sandbox_id=? order by task_id',(sid,))]
def inventory():return rows("select b.sandbox_id,b.runtime_id,b.template_id,s.status,a.charged,a.state from runtime_binding b join sandbox s on s.id=b.sandbox_id join cube_admission a on a.runtime_id=b.runtime_id where a.worker_id='vps' and a.state<>'deleted'")
def plan():
    current=inventory();lookup={r['sandbox_id']:r for r in current};active=[r for r in current if r['charged']]
    assert all(r['status']=='running' and r['state']=='active' for r in active),'Unsettled runtime operation'
    env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);policy=json.loads(env['SANDBOXD_CUBE_ADMISSION'])
    reviewed={'01M16MV7KZSF3YNAJ1VKWKYED5'}
    for scope in root.glob('vite-batch-*/scope.json'):
        reviewed.update(x['sandbox_id'] for x in json.loads(scope.read_text())['selected'])
    candidates=[]
    for journal in (root/'recovery-moves').iterdir():
        if not (journal/'complete.json').exists() or not list(journal.glob('module-health-*.json')):continue
        receipt=json.loads((journal/'complete.json').read_text());sid=receipt['sandbox_id'];row=lookup.get(sid)
        if sid not in reviewed or not row or row['status']!='stopped' or row['charged'] or row['state']!='released':continue
        job=json.loads((journal/'worker-job.PRIVATE.json').read_text())
        if job['runtime_id']!=row['runtime_id'] or task_ids(sid)!=job['source']['task_ids']:continue
        candidates.append({**row,'journal':str(journal),'job':job})
    candidates.sort(key=lambda r:(policy['templates'][r['template_id']]['memory_mb'],r['sandbox_id']))
    spec=importlib.util.spec_from_file_location('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
    for row in active:
        sid=row['sandbox_id'];origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
        port=rows('select web_port from sandbox where id=?',(sid,))[0]['web_port'] or 3000
        row['job']={'worker':'vps','id':'density-existing-'+sid,'sandbox_id':sid,'runtime_id':row['runtime_id'],'headers':headers,'web_port':port}
    needed=50-len(active);assert 0<needed<=50
    selected=candidates[:needed];total=active+selected
    # Match cube.VMOverheadMB: admission charges the entire guest limit plus
    # 128 MiB for each VM, even when its measured resident footprint is lower.
    memory=sum(policy['templates'][r['template_id']]['memory_mb']+128 for r in total)
    cpu=sum(policy['resource_budget']['profiles'][r['template_id']]['cpu_millis'] for r in total)
    assert memory<=policy['resource_budget']['memory_mb'] and cpu<=policy['resource_budget']['cpu_millis']
    return env,active,selected,{'target_total_running':50,'already_running':len(active),'eligible_stopped':len(candidates),'needed':needed,'ready':len(selected)==needed,'reserved_memory_mb':memory,'weighted_cpu_millis':cpu,'model_calls':False}
guest_sample={};guest_sample_at=0
def pressure():
    global guest_sample,guest_sample_at
    if time.monotonic()-guest_sample_at>5:
        code="import pathlib,json;p=pathlib.Path;m={l.split(':',1)[0]:int(l.split()[1])*1024 for l in p('/proc/meminfo').read_text().splitlines()};v=dict(l.split() for l in p('/proc/vmstat').read_text().splitlines());f=next(l for l in p('/proc/pressure/memory').read_text().splitlines() if l.startswith('full '));print(json.dumps({'available_bytes':m['MemAvailable'],'oom_kill':int(v['oom_kill']),'full_psi_avg10':float(dict(x.split('=') for x in f.split()[1:])['avg10'])}))"
        guest_sample=json.loads(subprocess.check_output(SSH+['python3 -'],input=code.encode(),timeout=20));guest_sample_at=time.monotonic()
    mem={l.split(':',1)[0]:int(l.split()[1])*1024 for l in P('/proc/meminfo').read_text().splitlines()}
    full=next(l for l in P('/proc/pressure/memory').read_text().splitlines() if l.startswith('full '));avg=float(dict(x.split('=') for x in full.split()[1:])['avg10'])
    vm=dict(l.split() for l in P('/proc/vmstat').read_text().splitlines())
    return {'available_bytes':mem['MemAvailable'],'full_psi_avg10':avg,'oom_kill':int(vm['oom_kill']),'worker':guest_sample}
def save(name,value):b.atomic(run_dir/name,b.encoded(value))
if sys.argv[1:]==['--plan']:
    print(json.dumps(plan()[3]));sys.exit(0)
assert sys.argv[1:]==['--run']
started=[];selected=[];gate=None;cleanup=[];own_activity={};warmed=[]
with b.locked():
    canary=json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text());assert canary['passed'] and canary['revision']=='2c7e700'
    assert not barrier.exists(),'Another reviewed restore batch is active'
    assert not rows("select id from cube_relocation where phase='fenced'")
    assert not rows("select task_id from task where status in ('running','queued')"),'User work is active'
    env,active,selected,scope=plan();assert scope['ready'],'Not enough reviewed apps yet'
    before=pressure();assert before['available_bytes']>8*G and before['full_psi_avg10']<1
    run_dir.mkdir(mode=0o700,exist_ok=False)
    bindings={r['sandbox_id']:r['runtime_id'] for r in inventory()}
    initial_tasks={r['sandbox_id']:task_ids(r['sandbox_id']) for r in selected}
    gate={'purpose':'reviewed-restore-barrier','allowed_sandboxes':[],'owner':run_dir.name};b.atomic(barrier,b.encoded(gate))
    save('scope.json',{**scope,'selected':[r['sandbox_id'] for r in selected],'existing_running':[r['sandbox_id'] for r in active],'before':before})
    token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
    def api(sid,action):
        req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
        with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
    def guard():
        now=pressure();save('last-pressure.json',now)
        assert now['worker']['oom_kill']==before['worker']['oom_kill'] and now['worker']['available_bytes']>2*G and now['worker']['full_psi_avg10']<5,'Worker pressure guard'
        assert now['oom_kill']==before['oom_kill'] and now['available_bytes']>8*G and now['full_psi_avg10']<5,'Host pressure guard'
        assert not rows("select task_id from task where status in ('running','queued')"),'User work started; stop load test'
        with urllib.request.urlopen('http://127.0.0.1:9090/readyz',timeout=5) as r:assert r.read().strip()==b'ready'
    def probe(row):
        worker=transport.Worker(row['job']);headers={**worker.headers,'Host':worker.headers['Host'].replace('3031-',str(row['job']['web_port'])+'-',1)}
        began=time.monotonic();worker.http('GET','/',headers=headers,timeout=5)
        return {'sandbox_id':row['sandbox_id'],'seconds':time.monotonic()-began}
    def warm(row):
        worker=transport.Worker(row['job']);headers={**worker.headers,'Host':worker.headers['Host'].replace('3031-',str(row['job']['web_port'])+'-',1)}
        before_status=worker.control('GET','/status');assert not before_status['active_task']
        restarts={p['name']:p['restarts'] for p in before_status['processes']}
        html=worker.http('GET','/',headers=headers,timeout=10);queue=assets.entries(html);assert queue,'No reviewed Vite entry modules found'
        seen=set();total=0;began=time.monotonic()
        while queue:
            path=queue.pop(0)
            if path in seen:continue
            seen.add(path);assert len(seen)<=512,'Module graph exceeds reviewed test bound'
            if len(seen)%8==1:guard()
            data=worker.http('GET',path,headers=headers,timeout=30);total+=len(data);assert total<=64*1024**2,'Module graph byte limit'
            queue.extend(p for p in assets.imports(path,data) if p not in seen)
        state=worker.control('GET','/status')
        assert not state['active_task'] and all(p['running'] and p['restarts']==restarts[p['name']] for p in state['processes']),'Process restarted during module compilation'
        memory=assets.guest_memory(row['runtime_id']);assert memory['oom_kill']==0,'Guest OOM during preview load'
        warmed.append({'memory':memory,'sandbox_id':row['sandbox_id'],'modules':len(seen),'bytes':total,'seconds':time.monotonic()-began})
        save('module-warmup.json',warmed)
    def interrupted(*args):raise KeyboardInterrupt('operator interrupted preview test')
    signal.signal(signal.SIGTERM,interrupted)
    with account_maintenance([r['sandbox_id'] for r in selected],run_dir):
        try:
            for row in selected:
                guard();started.append(row['sandbox_id']);save('start-intents.json',started)
                api(row['sandbox_id'],'start')
                own_activity[row['sandbox_id']]=rows('select last_active_at from sandbox where id=?',(row['sandbox_id'],))[0]['last_active_at']
                update=subprocess.run(SSH+['python3','/opt/baarcha-vps-export-recovery-2c7e700/worker.py','--container',row['runtime_id']],capture_output=True,timeout=460)
                receipts=[json.loads(line) for line in update.stdout.splitlines()]
                assert update.returncode==0 and len(receipts)==1 and receipts[0]['status'] in ('updated','current'),'Supervisor update requires reconciliation'
                save(row['sandbox_id']+'-supervisor.json',receipts[0])
                deadline=time.monotonic()+60
                while True:
                    try:probe(row);break
                    except (OSError,RuntimeError):assert time.monotonic()<deadline;time.sleep(1)
                warm(row)
                save('progress.json',{'started':len(started),'total_requested':len(selected),'at':time.time()})
            live=[r for r in inventory() if r['charged']];assert len(live)==50 and all(r['status']=='running' for r in live)
            save('fifty-running.json',{'count':len(live),'runtime_ids':[r['runtime_id'] for r in live],'at':time.time()})
            measurements=[]
            for _ in range(12):
                guard()
                with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:measurements.extend(pool.map(probe,selected+active))
                time.sleep(5)
            wanted=[r['runtime_id'] for r in live]
            code=textwrap.dedent("""
    import pathlib,subprocess,json
    wanted=set(WANTED);p=pathlib.Path;items=[]
    raw=subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list'],text=True)
    for line in raw.splitlines()[1:]:
     a=line.split()
     if len(a)<3 or a[0] not in wanted:continue
     v={l.split(':',1)[0]:int(l.split()[1])*1024 for l in (p('/proc')/a[1]/'smaps_rollup').read_text().splitlines() if l.split(':',1)[0] in ('Rss','Pss')}
     items.append({'runtime_id':a[0],'rss_bytes':v['Rss'],'pss_bytes':v['Pss']})
    m={l.split(':',1)[0]:int(l.split()[1])*1024 for l in p('/proc/meminfo').read_text().splitlines()}
    print(json.dumps({'runtimes':items,'guest_available_bytes':m['MemAvailable'],'guest_memory_pressure':p('/proc/pressure/memory').read_text()}))
    """).replace('WANTED',repr(wanted))
            native=json.loads(subprocess.check_output(SSH+['python3 -'],input=code.encode(),timeout=30));assert len(native['runtimes'])==50
            save('runtime-memory.json',native);times=sorted(r['seconds'] for r in measurements);pss=sorted(r['pss_bytes'] for r in native['runtimes'])
            result={'passed':True,'concurrent_running':50,'http_checks':len(times),'http_p95_seconds':times[int(.95*(len(times)-1))],'http_max_seconds':max(times),'total_pss_bytes':sum(pss),'median_pss_bytes':statistics.median(pss),'max_pss_bytes':max(pss),'model_calls':False,'module_http_checks':sum(r['modules'] for r in warmed),'module_bytes':sum(r['bytes'] for r in warmed),'application_scope':'HTML and local Vite module graph serving; no browser JS execution, model requests or coding tasks','after':pressure()}
            save('result.json',result);print(json.dumps(result),flush=True)
        except BaseException as error:
            save('failed.json',{'type':type(error).__name__,'reason':str(error)[:300],'started':len(started),'at':time.time()});raise
        finally:
            for sid in reversed(started):
                activity=rows('select last_active_at from sandbox where id=?',(sid,))[0]['last_active_at']
                if task_ids(sid)!=initial_tasks[sid] or (sid in own_activity and activity>own_activity[sid]):cleanup.append({'sandbox_id':sid,'preserved_for_user_work':True});continue
                try:api(sid,'stop');cleanup.append({'sandbox_id':sid,'stopped':True})
                except Exception as error:cleanup.append({'sandbox_id':sid,'error':type(error).__name__})
            current={r['sandbox_id']:r['runtime_id'] for r in inventory()}
            final={'runtimes':cleanup,'bindings_preserved':all(current.get(k)==v for k,v in bindings.items()),'existing_running_preserved':all(r['sandbox_id'] not in started for r in active),'complete':all(r.get('stopped') or r.get('preserved_for_user_work') for r in cleanup)}
            save('cleanup.json',final)
            if final['complete'] and final['bindings_preserved'] and gate is not None and json.loads(barrier.read_text())==gate:barrier.unlink()
            assert final['complete'] and final['bindings_preserved'],'Preview cleanup requires reconciliation; barrier retained'
