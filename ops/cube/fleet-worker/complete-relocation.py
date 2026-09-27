#!/usr/bin/env python3
"""Resume verification of an already restored target; never reimport or create."""
import importlib.util,json,sys,time
from pathlib import Path
ROOT=Path('/opt/baarcha-bench/cube-fleet-20260927/capacity-ready')
s=importlib.util.spec_from_file_location('move',ROOT/'relocation-canary.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
with m.r.b.locked():
    job=ROOT/'moves'/sys.argv[1];assert job.parent==ROOT/'moves' and job.is_dir()
    scope=json.loads((job/'scope.json').read_text());sid=scope['sandbox_id']
    assert m.c.rows('SELECT phase FROM cube_relocation WHERE id=?',(job.name,))[0]['phase']=='fenced'
    old=job/'worker-result.json';assert old.exists() and json.loads(old.read_text()).get('failed')
    assert not (job/'worker-content.json').exists(),'review already-started target separately'
    old.rename(job/('worker-result.failed-'+str(int(time.time()))+'.json'))
    request=json.loads((job/'worker-job.PRIVATE.json').read_text());request['action']='verify';m.save(job/'worker-job.PRIVATE.json',request)
    deadline=time.monotonic()+1800
    while not (job/'worker-content.json').exists():
        assert not old.exists(),'content verification failed'
        assert time.monotonic()<deadline;time.sleep(1)
    with m.target_channel(job):
        while not old.exists():
            assert time.monotonic()<deadline;time.sleep(1)
        result=json.loads(old.read_text());assert not result.get('failed'),str(result);m.save(job/'verified.json',result)
    source=json.loads((job/'export-result.PRIVATE.json').read_text())
    status,raw=m.c.request('127.0.0.1',20300,'/sandboxes/'+source['runtime_id'],headers={'X-API-Key':m.c.ENV['SANDBOXD_CUBE_API_KEY']})
    assert status==200 and json.loads(raw)['state']=='paused'
    m.cli('commit',job)
    status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200
    result=dict(moved=True,sandbox_id=sid,source_runtime_id=source['runtime_id'],target_runtime_id=request['runtime_id'],preview=m.preview(sid,scope.get('marker','')))
    status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
    started=time.monotonic();status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200
    result['wake_seconds']=time.monotonic()-started;result['after_wake']=m.preview(sid,scope.get('marker',''))
    status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
    m.save(job/'acceptance.json',result);print(json.dumps(result),flush=True)
