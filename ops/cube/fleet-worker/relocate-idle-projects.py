#!/usr/bin/env python3
"""Explicit idle project cohort. Keep each source until verified cutover."""
import collections,concurrent.futures,importlib.util,json,secrets,sys,threading,time,traceback
from pathlib import Path
ROOT=Path('/opt/baarcha-bench/cube-fleet-20260927/capacity-ready')
s=importlib.util.spec_from_file_location('move',ROOT/'relocation-canary.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
SOURCE_SLOTS=threading.BoundedSemaphore(2)

def relocate(sid):
    row=m.c.rows("SELECT s.status,a.name,a.external_user_id FROM sandbox s JOIN app a ON a.id=s.app_id WHERE s.id=?",(sid,))[0]
    assert row['status']=='stopped','only idle projects are eligible'
    assert not m.c.rows("SELECT task_id FROM task WHERE sandbox_id=? AND status='running'",(sid,))
    job=ROOT/'moves'/('project-'+sid+'-'+secrets.token_hex(4));job.mkdir(mode=0o700)
    m.save(job/'scope.json',dict(sandbox_id=sid,name=row['name'],started_at=time.time()))
    print(json.dumps(dict(phase='starting',sandbox_id=sid,name=row['name'],job=job.name)),flush=True)
    started=time.monotonic()
    SOURCE_SLOTS.acquire();source_slot=True
    def source_stopped():
        nonlocal source_slot
        if source_slot:SOURCE_SLOTS.release();source_slot=False
    try:
        status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200,'source start failed'
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
            export=pool.submit(m.worker_export,sid,job)
            while True:
                try:source=export.result(timeout=15);break
                except concurrent.futures.TimeoutError:
                    status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200,'source heartbeat failed'
        source,target=m.move(sid,job,source,source_stopped)
        result=dict(moved=True,sandbox_id=sid,name=row['name'],target_runtime_id=target,seconds=time.monotonic()-started,preview=m.preview(sid,''))
        status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
        wake=time.monotonic();status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200
        result['wake_seconds']=time.monotonic()-wake;result['after_wake']=m.preview(sid,'')
        # Return an originally idle project to idle; its assigned worker is B200.
        status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
        m.save(job/'acceptance.json',result);print(json.dumps(result),flush=True)
        return result
    except Exception as error:
        fenced=m.c.rows("SELECT phase FROM cube_relocation WHERE id=?",(job.name,))
        if not fenced:
            # Export refusal must not strand an original in quiescence.
            status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/start')
            if status==200:
                (host,port),headers=m.c.client(sid)
                status,_=m.c.request(host,port,'/workspace/resume','POST',headers=headers)
                assert status==200,'source resume failed'
                status,_=m.c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
        m.save(job/'failure.json',dict(error=type(error).__name__,line=traceback.extract_tb(error.__traceback__)[-1].lineno,fenced=fenced))
        raise
    finally:source_stopped()

if __name__=='__main__':
    with m.r.b.locked():
        args=sys.argv[1:];parallel=1
        if args and args[0] in ['--parallel=2','--parallel=4']:parallel=int(args[0][-1]);args=args[1:]
        keep=[]
        if args and args[0].startswith('--keep-vps='):keep=args[0].split('=',1)[1].split(',');args=args[1:]
        if args==['--remaining']:
            assert not m.c.rows("SELECT id FROM cube_relocation WHERE phase='fenced'"),'reconcile the preceding move first'
            args=[v['id'] for v in m.c.rows("SELECT s.id FROM sandbox s JOIN app a ON a.id=s.app_id JOIN runtime_binding b ON b.sandbox_id=s.id JOIN cube_admission p ON p.runtime_id=b.runtime_id WHERE p.worker_id='vps' AND s.status='stopped' AND coalesce(a.external_user_id,'') NOT LIKE 'operator:%' AND a.name NOT LIKE 'minecraft-tunnel%' AND NOT EXISTS(SELECT 1 FROM task t WHERE t.sandbox_id=s.id AND t.status='running') ORDER BY CASE WHEN EXISTS(SELECT 1 FROM runtime_migration m WHERE m.sandbox_id=s.id) THEN 0 ELSE 1 END,a.name")]
            args=[sid for sid in args if sid not in keep]
            m.save(ROOT/('cohort-'+str(int(time.time()))+'.json'),dict(ids=args,parallel=parallel,kept_on_vps=keep))
            print(json.dumps(dict(phase='cohort-selected',projects=len(args),parallel=parallel)),flush=True)
        queue=collections.deque(args);guard=threading.Lock();halt=threading.Event()
        def consume():
            while not halt.is_set():
                with guard:
                    if not queue:return
                    sid=queue.popleft()
                try:relocate(sid)
                except Exception:halt.set();raise
        with concurrent.futures.ThreadPoolExecutor(max_workers=parallel) as pool:
            futures=[pool.submit(consume) for _ in range(parallel)]
            for future in futures:future.result()
