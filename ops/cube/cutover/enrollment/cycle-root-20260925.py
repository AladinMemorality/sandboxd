#!/usr/bin/env python3
"""Finish reviewed empty-worker cycle with seamless ownership of four flocks."""
import argparse, contextlib, ctypes, fcntl, hashlib, importlib.util, json, os, pathlib, signal, sqlite3, subprocess, time
P=pathlib.Path
BASE=P('/opt/baarcha-bench/cube-host-lifecycle-enrollment-20260925')
JOB=BASE/'execute-live-01'
SOURCE=BASE/'cube-host-enrollment-execute-check06.py'
SOURCE_SHA='ca6dea40bba2e4f3cc15ecfba122b18ad69fc4ece17567f45aac224ce30562da'
PRIOR=BASE/'resume-root-02.py'
PRIOR_SHA='d6b3ae4f1be572a974d6af915ae21c144e77846d1a48c6ab40431a50cf0f3472'

def need(v,msg):
    if not v:raise RuntimeError(msg)
def read(n):return json.loads((JOB/n).read_text())
def sha(p):return hashlib.sha256(P(p).read_bytes()).hexdigest()
def module(p,n):
    s=importlib.util.spec_from_file_location(n,p);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m

def own_locks(m, fds):
    for path,fd in zip(m.LOCKS,fds):
        a=P(path).stat();b=os.fstat(fd)
        need((a.st_dev,a.st_ino)==(b.st_dev,b.st_ino),'inherited lock inode differs')
        need('FLOCK  ADVISORY  WRITE' in P('/proc/self/fdinfo/'+str(fd)).read_text(),'inherited exclusive lock absent')
        code='import os,fcntl,sys\nf=os.open('+repr(path)+',os.O_RDWR)\ntry:fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)\nexcept BlockingIOError:sys.exit(0)\nsys.exit(1)'
        m.run(['/usr/bin/python3','-c',code])

def main():
    p=argparse.ArgumentParser();p.add_argument('--sha256',required=True);p.add_argument('--execute',action='store_true');a=p.parse_args()
    need(os.geteuid()==0 and sha(__file__)==a.sha256,'exact root-reviewed helper required')
    need(sha(SOURCE)==SOURCE_SHA and sha(PRIOR)==PRIOR_SHA,'reviewed source changed')
    old=module(PRIOR,'prior');m=module(SOURCE,'enrollment');old.locks(m)
    need(read('status.json')['phase']=='first-supervisor-ready','wrong continuation boundary')
    need(read('continuation-failure.json')['message']=='command failed: baarcha-cube-worker-stop','different prior failure')
    need(not m.MARKER.exists(),'unexpected stop marker')
    need(m.run(['git','-C','/opt/baarcha/app/landing','rev-parse','HEAD']).decode().strip()==m.PLATFORM,'platform drift')
    need(sha(m.SCRIPT)==m.HOST_SHA and sha('/usr/local/libexec/baarcha-cube-worker-stop')==m.STOP_SHA and sha('/usr/local/libexec/baarcha-cube-worker-start')==m.START_SHA,'installed helpers differ')
    need(sha('/var/lib/sandboxd/secrets.key')==read('controller-backup.json')['key_sha256'],'controller key changed')
    cp=m.inspect_cp();need(not cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name']=='no','controller fence lost')
    need(json.loads(m.http('http://127.0.0.1:2019/config/'))==json.loads((m.ROUTING/'offline.json').read_text()),'offline routes lost')
    need(all(m.unit(t)['ActiveState']=='inactive' and m.unit(t.replace('.timer','.service'))['ActiveState']=='inactive' for t in m.TIMERS),'timer writer active')
    status=json.loads((m.WORKER/'lifecycle-status.json').read_text());need(status['state']=='running-unreconciled' and status['qemu_pid']==3020562,'worker generation differs')
    task=m.Enrollment(JOB,True);task.stop=json.loads((m.CONF/'worker-stop.json').read_text());task.baseline=read('baseline.json')
    need(task.stop['qemu_pid']==3020562 and task.stop['qemu_start_time']==m.starttime(3020562),'stop configuration generation differs')
    expected=task.baseline['containers'];change=read('continuation-reviewed.json')['reviewed_drain_identity_transition']
    need(change['before']==[{'id':old.OLD,'image':old.APP_IMAGE,'name':old.APP_CONTAINER}] and change['after']==[{'id':old.NEW,'image':old.APP_IMAGE,'name':old.APP_CONTAINER}],'reviewed delta differs')
    expected=sorted([x for x in expected if x not in change['before']]+change['after'],key=lambda x:x['id'])
    need(m.container_identities()==expected,'new container identity drift');task.baseline['containers']=expected
    # Close observation connections deterministically; the frozen predecessor
    # left them alive through sqlite3.Connection's context manager.
    def closed_observation():
        with contextlib.closing(sqlite3.connect('file:'+str(m.DB)+'?mode=ro',uri=True,timeout=2)) as db:
            db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
            tasks=dict(db.execute('SELECT status,count(*) FROM task GROUP BY status'))
            count=db.execute('SELECT count(*) FROM runtime_binding').fetchone()[0]
            return {'task_status_counts':tasks,'runtime_bindings':count,'active_tasks':sum(tasks.get(x,0) for x in ('running','starting','queued','pending'))}
    task.observer.sqlite_observation=closed_observation
    task.changed=True;task.worker_touched=True;task.shutdown_since=read('preflight-passed.json')['at']
    task.nested=m.NestedLock(task.stop['worker_boot_id']);task.nested_ready()
    original_event=task.event
    task.event=lambda name,value:original_event('cycle-check-'+str(os.getpid())+'-'+name,value)
    try:task.verify_locks()
    finally:task.event=original_event
    fds=[];handed=False
    try:
        lib=ctypes.CDLL(None,use_errno=True);pidfd=os.pidfd_open(old.PID,0)
        try:
            for original in range(3,7):
                fd=lib.syscall(438,pidfd,original,0)
                need(fd>=0,'lock descriptor duplication refused');os.set_inheritable(fd,False);fds.append(fd)
        finally:os.close(pidfd)
        own_locks(m,fds);old.locks(m)
        print(json.dumps({'cycle_preflight':True,'same_open_file_descriptions_held':True,'execute':a.execute}),flush=True)
        if not a.execute:return
        for sig in (signal.SIGINT,signal.SIGTERM,signal.SIGHUP):signal.signal(sig,lambda *_:print('Signal noted; holding maintenance locks',flush=True))
        task.event('lock-handoff-intent',{'old_pid':old.PID,'old_start':old.START,'new_pid':os.getpid(),'method':'pidfd_getfd exact four lock descriptors','database_descriptors_copied':False,'helper_sha256':a.sha256})
        # Duplicates share the locked open file descriptions before old exit.
        # The standard deployment lock is never released during this handoff.
        handed=True
        m.run(['/usr/bin/python3',str(SOURCE),'--script-sha256',SOURCE_SHA,'--job',str(JOB),'--request-recovery','release-after-manual-recovery'])
        m.wait_for(lambda:not P('/proc/'+str(old.PID)).exists(),20);own_locks(m,fds)
        task.event('lock-handoff-complete',{'old_process_exited':True,'four_independent_lock_contenders_refused':True})
        # ReadInventory will independently refuse any remaining database reader.
        task.coordinated_stop();own_locks(m,fds)
        task.boot(task.stop['worker_boot_id'],'second-supervisor-ready');own_locks(m,fds)
        value=json.loads(m.run(['/usr/local/libexec/baarcha-cube-worker-start'],180));need(value['tenant_ready'] and not m.MARKER.exists(),'startup reconciliation failed')
        task.event('startup-reconciled',value)
        need(m.container_identities()==expected,'closed identities changed')
        need(m.reviewed_hold(m.unit(m.UNIT))==task.baseline['outer_boot_hold'],'hold/enablement drift')
        task.event('retained-orphan-after',m.orphan_stat());task.event('container-identity-before-reopen',{'unchanged_since_closed_state':True,'reviewed_drain_transition':True})
        own_locks(m,fds);task.restore_traffic();task.advance('complete',{'global_cube_enabled':False,'unit_enabled_at_boot':False,'real_coordinator_cycle':True,'reviewed_continuations':True})
    except Exception as error:
        try:task.event('cycle-continuation-failure',{'error_class':type(error).__name__,'message':str(error) if isinstance(error,RuntimeError) else 'bounded operation failed','phase':task.phase,'locks_handed_off':handed})
        except Exception:print('Failure evidence unavailable; retaining maintenance ownership',flush=True)
        if handed:
            print('Failure retained; new owner holds all maintenance locks',flush=True)
            while True:time.sleep(1)
        raise
    finally:
        if task.nested is not None:task.nested.close()
        for fd in fds:os.close(fd)

if __name__=='__main__':main()
