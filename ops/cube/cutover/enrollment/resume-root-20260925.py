#!/usr/bin/env python3
"""One-use continuation; original reviewed runner retains its four locks."""
import argparse, datetime, hashlib, importlib.util, json, os, pathlib, re, sqlite3, stat, subprocess
P = pathlib.Path
BASE = P('/opt/baarcha-bench/cube-host-lifecycle-enrollment-20260925')
SOURCE = BASE/'cube-host-enrollment-execute-check06.py'
SOURCE_SHA = 'ca6dea40bba2e4f3cc15ecfba122b18ad69fc4ece17567f45aac224ce30562da'
JOB = BASE/'execute-live-01'
PID = 3009710
START = '483280214'
OLD = '23392f42f524238519eccb26168866a8d7c667bee9c0632a41bc09024fc3ba8d'
NEW = '02503dfa5cf882692436fcef4fa6e049b0480441b9347e506d2dec225cefedb0'
APP_CONTAINER = '/s-01M3CKN99ZF90BEEA4DS66YAQV'
APP_IMAGE = 'sha256:f05e2d5103acbc1bda4ba22d618eabba93307f914dd26bf2f2f3f4163c458434'

def need(value, message):
    if not value: raise RuntimeError(message)

def sha(path): return hashlib.sha256(P(path).read_bytes()).hexdigest()
def read(name): return json.loads((JOB/name).read_text())

def locks(m):
    root=P('/proc')/str(PID)
    need(root.exists() and m.starttime(PID)==START, 'original lock holder generation changed')
    need((root/'exe').resolve()==P('/usr/bin/python3.12'), 'wrong lock holder')
    need(str(SOURCE).encode() in (root/'cmdline').read_bytes().split(b'\0'), 'wrong lock holder script')
    expected=[(2306,8787626),(2306,8947519),(27,11),(2306,9211517)]
    for index,(path,identity) in enumerate(zip(m.LOCKS,expected),3):
        s=P(path).stat(); f=root/'fd'/str(index); t=f.stat()
        need((s.st_dev,s.st_ino)==identity==(t.st_dev,t.st_ino), 'lock inode changed')
        need(re.search(r'FLOCK\s+ADVISORY\s+WRITE\s+'+str(PID)+r'\s', (root/'fdinfo'/str(index)).read_text()), 'exclusive lock lost')

def main():
    p=argparse.ArgumentParser();p.add_argument('--sha256',required=True);p.add_argument('--execute',action='store_true');a=p.parse_args()
    need(os.geteuid()==0 and sha(__file__)==a.sha256, 'exact root-reviewed continuation required')
    need(sha(SOURCE)==SOURCE_SHA, 'frozen source differs')
    spec=importlib.util.spec_from_file_location('enrollment',SOURCE);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
    locks(m)
    need(sha(m.RELEASE/'source/ops/cube/worker-lifecycle/lifecycle.py')==m.HOST_SHA and sha(m.RELEASE/'binaries/cube-worker-stop')==m.STOP_SHA and sha(m.RELEASE/'binaries/cube-worker-start')==m.START_SHA,'reviewed helper or coordinator changed')
    need(sha('/var/lib/sandboxd/secrets.key')==read('controller-backup.json')['key_sha256'],'controller key changed')
    failure=read('failure.json')
    need(failure['phase']=='data-disk-expanded' and failure['error_class']=='FileNotFoundError' and failure['worker_touched'] and not failure['marker_present'], 'failure boundary differs')
    need(not P('/proc/'+str(m.INITIAL_PID)).exists() and not m.MARKER.exists(), 'worker or stop marker present')
    need(m.unit(m.UNIT)['ActiveState']=='inactive' and m.unit(m.UNIT)['MainPID']=='0', 'worker not stopped')
    cp=m.inspect_cp();need(not cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name']=='no','controller not fenced')
    need(m.db_counts()=={'apps':67,'bindings':0,'admission':0,'recovery':0,'active':0}, 'canonical counts changed')
    need(m.pg_counts()['thumbnail']==0,'capture writer exists')
    need(json.loads(m.http('http://127.0.0.1:2019/config/'))==json.loads((m.ROUTING/'offline.json').read_text()),'offline routes changed')
    need(all(m.unit(t)['ActiveState']=='inactive' and m.unit(t.replace('.timer','.service'))['ActiveState']=='inactive' for t in m.TIMERS),'writers not inactive')
    need(m.run(['git','-C','/opt/baarcha/app/landing','rev-parse','HEAD']).decode().strip()==m.PLATFORM,'platform changed')
    need(read('manual-retained-stop.json')['stopped'] and read('data-disk-expanded.json')['virtual_bytes']==448*1024**3,'shutdown or expansion evidence missing')
    for path in [m.SCRIPT,'/usr/local/libexec/backup_monitor.py','/usr/local/libexec/baarcha-cube-worker-stop','/usr/local/libexec/baarcha-cube-worker-start',m.CONF/'lifecycle.json',m.CONF/'worker-stop.json',m.WORKER/'lifecycle-status.json']:
        need(not P(path).exists() and not P(path).is_symlink(),'partial installation exists')
    parent=P('/usr/local');s=parent.stat()
    need(parent.resolve()==parent and s.st_uid==0 and stat.S_IMODE(s.st_mode)==0o755 and not P('/usr/local/libexec').exists(),'installation parent differs')
    before=read('baseline.json');current=m.container_identities()
    removed=[x for x in before['containers'] if x not in current];added=[x for x in current if x not in before['containers']]
    need(removed==[{'id':OLD,'image':APP_IMAGE,'name':APP_CONTAINER}] and added==[{'id':NEW,'image':APP_IMAGE,'name':APP_CONTAINER}],'unreviewed container identity drift')
    d=json.loads(m.run(['docker','inspect',NEW]))[0]
    created=datetime.datetime.fromisoformat(d['Created'][:26]+'+00:00')
    lower=datetime.datetime.fromisoformat(read('preflight-passed.json')['at']);upper=datetime.datetime.fromisoformat(read('controller-fenced.json')['at'])
    need(lower<created<upper,'replacement was not during drain')
    for db in [m.DB,JOB/'controller-before-worker.sqlite']:
        with sqlite3.connect('file:'+str(db)+'?mode=ro',uri=True) as c:
            need(c.execute('SELECT container_id FROM sandbox WHERE id=?',('01M3CKN99ZF90BEEA4DS66YAQV',)).fetchall()==[(NEW,)],'closed/current replacement binding differs')
    need(sha(JOB/'controller-before-worker.sqlite')==read('controller-backup.json')['sha256'],'closed backup changed')
    disk=P('/mnt/nvme/baarcha-cube/worker-01/data.qcow2');ds=disk.stat()
    prior=next(x for x in json.loads((m.REVIEW/'qemu-and-disks.json').read_text())['disks'] if x['path']==str(disk))
    need(disk.resolve()==disk and (ds.st_dev,ds.st_ino)==(prior['device'],prior['inode']),'disk identity changed')
    info=json.loads(m.run(['qemu-img','info','--output=json',str(disk)]));need(info['virtual-size']==448*1024**3 and info['format']=='qcow2' and not info.get('backing-filename'),'expanded disk differs')
    m.run(['qemu-img','check','-f','qcow2',str(disk)],120)
    locks(m)
    print(json.dumps({'continuation_preflight':True,'execute':a.execute}),flush=True)
    if not a.execute:return
    task=m.Enrollment(JOB,True);task.baseline=before;task.baseline['containers']=current
    task.changed=True;task.worker_touched=True;task.shutdown_since=read('preflight-passed.json')['at']
    task.event('continuation-reviewed',{'runner_pid':PID,'runner_start':START,'source_sha256':SOURCE_SHA,'continuation_sha256':a.sha256,'reviewed_drain_identity_transition':{'before':removed,'after':added}})
    P('/usr/local/libexec').mkdir(mode=0o755);os.chown('/usr/local/libexec',0,0);os.chmod('/usr/local/libexec',0o755)
    directory=P('/usr/local/libexec');ds=directory.stat()
    need(directory.resolve()==directory and ds.st_uid==ds.st_gid==0 and stat.S_ISDIR(ds.st_mode) and stat.S_IMODE(ds.st_mode)==0o755,'helper directory verification failed')
    try:
        locks(m);task.install();locks(m)
        boot=task.boot(m.INITIAL_BOOT,'first-supervisor-ready');locks(m);task.coordinated_stop();locks(m)
        task.boot(boot,'second-supervisor-ready');locks(m)
        value=json.loads(m.run(['/usr/local/libexec/baarcha-cube-worker-start'],180))
        need(value['tenant_ready'] and not m.MARKER.exists(),'startup reconciliation failed')
        task.event('startup-reconciled',value)
        need(m.container_identities()==current,'closed container identities changed')
        task.event('container-identity-before-reopen',{'unchanged_since_closed_state':True,'reviewed_drain_transition':True})
        need(m.reviewed_hold(m.unit(m.UNIT))==before['outer_boot_hold'],'hold or enablement drift')
        task.event('retained-orphan-after',m.orphan_stat());locks(m);task.restore_traffic()
        task.advance('complete',{'global_cube_enabled':False,'unit_enabled_at_boot':False,'real_coordinator_cycle':True,'continuation_after_missing_directory':True})
    except Exception as error:
        task.event('continuation-failure',{'error_class':type(error).__name__,'message':str(error) if isinstance(error,RuntimeError) else 'bounded operation failed','phase':task.phase,'original_locks_retained':True})
        raise
    finally:
        if task.nested is not None:task.nested.close()
    locks(m)
    print(json.dumps({'continuation_complete':True,'original_locks_still_held':True}),flush=True)

if __name__=='__main__':main()
