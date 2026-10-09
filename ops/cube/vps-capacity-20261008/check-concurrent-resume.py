"""Exercise overlapping real VPS starts without model calls or new VMs."""
import concurrent.futures,contextlib,importlib.util,json,os,pathlib,sqlite3,subprocess,threading,time,urllib.request
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
ids=['01M16MV7KZSF3YNAJ1VKWKYED5','01M17DFXSNY0CDDQ8EN0TBE6G3']
out=root/'concurrent-resume-7745f34'
def rows(sql,args=()):
    with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:return db.execute(sql,args).fetchall()
def state(sid):return rows('select s.status,a.state,a.charged,b.runtime_id,s.last_active_at from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))[0]
with b.locked():
    assert json.loads((root/'resume-retry-release-7745f34/deployed.json').read_text())['deployed']
    assert not rows("select task_id from task where status in ('queued','running')")
    before={sid:state(sid) for sid in ids};assert all(r[:3]==('stopped','released',0) for r in before.values())
    out.mkdir(mode=0o700)
    env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
    def api(sid,action):
        req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
        with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
    results=[];started=set();activity={}
    try:
        for round in range(3):
            gate=threading.Barrier(len(ids))
            def start(sid):
                started.add(sid);gate.wait();began=time.monotonic();api(sid,'start')
                activity[sid]=state(sid)[4]
                return {'sandbox_id':sid,'round':round+1,'seconds':time.monotonic()-began}
            with concurrent.futures.ThreadPoolExecutor(max_workers=len(ids)) as pool:results.extend(pool.map(start,ids))
            assert all(state(sid)[:3]==('running','active',1) for sid in ids)
            assert not rows("select admission_key from cube_admission where state='pending'")
            for sid in ids:
                origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
                headers={**headers,'Host':headers['Host'].replace('3031-','3000-',1)}
                req=urllib.request.Request('http://127.0.0.1:20080/',headers=headers)
                with urllib.request.urlopen(req,timeout=15) as response:assert response.status==200;assert response.read()
            for sid in ids:
                assert not rows("select task_id from task where status in ('queued','running')")
                assert state(sid)[4]==activity[sid],'User activity changed; preserve runtime'
                api(sid,'stop');started.remove(sid)
        assert all(state(sid)[:4]==before[sid][:4] for sid in ids)
        result={'passed':True,'concurrent_starts':2,'rounds':3,'starts':results,'pending_after':0,'original_states_restored':True,'b200_contacted':False,'model_calls':False}
        b.atomic(out/'result.json',b.encoded(result));print(json.dumps(result),flush=True)
    finally:
        for sid in started:
            if sid in activity and state(sid)[4]==activity[sid] and not rows("select task_id from task where status in ('queued','running')"):api(sid,'stop')
