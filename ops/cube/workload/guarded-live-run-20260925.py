#!/usr/bin/env python3
"""Exact four-guest acceptance; production remains Docker. No limit changes."""
import contextlib,fcntl,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,threading,time
P=pathlib.Path
BASE=P('/opt/baarcha-bench/cube-storage-guard-reviewed-20260925')
STAGE=P('/opt/baarcha-bench/cube-workload-guarded-wrapper-20260925-01')
WORK=P('/opt/baarcha-bench/cube-workload-guarded-live-20260925-01')
BIN=BASE/'bin/cube-workload-storage.test'
CP='54f9d073a7c2739b55481d5bbbb80f22847cb1e1d3225fe5af9590f2f48de5f3'
BOOT='c1b590df-28d3-4222-b9a3-c0408c8ccc07'
def sha(p):return hashlib.sha256(P(p).read_bytes()).hexdigest()
def need(v,m):
    if not v:raise RuntimeError(m)
def main():
    os.umask(0o077)
    need(os.geteuid()==0 and not STAGE.exists() and not WORK.exists(),'fresh root-owned stages required')
    need(sha(BIN)=='c4859ef42dd4e046627f44c5d5284cd5ac328c56fbcdc4097bd9c8fa5214e428','workload binary changed')
    need(sha(BASE/'source.tar.gz')=='47e66429d78348e9b421c8c1aa1049c33899bae48dffaab1fdfe151501630e2a','source archive differs')
    need(sha(BASE/'control-plane/migrations/0034_cube_storage_guard.sql')=='b569dda008325e79e5df782abb20e6a80fbcfff26baf28e416d70338f5a2c218','storage migration differs')
    with contextlib.ExitStack() as stack:
        for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock']:
            p=P(name);need(p.resolve()==p and p.stat().st_uid==0,'unsafe lock')
            fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);stack.callback(os.close,fd);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
        cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
        env=dict(x.split('=',1) for x in cp['Config']['Env'] if '=' in x)
        need(cp['Id']==CP and cp['State']['Running'] and env.get('SANDBOXD_CUBE_ENABLED')=='false' and env.get('SANDBOXD_CUBE_REVERSE_EGRESS')=='false','production runtime drift')
        with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
            need(db.execute('SELECT count(*) FROM runtime_binding').fetchone()[0]==0 and db.execute('SELECT count(*) FROM cube_admission').fetchone()[0]==0,'canonical Cube allocation exists')
        status=json.loads(P('/opt/baarcha-cube/worker-01/lifecycle-status.json').read_text())
        need(status['state']=='running-unreconciled' and status['qemu_pid']==3026420,'reviewed QEMU changed')
        need(P('/proc/3026420/stat').read_text().rsplit(')',1)[1].split()[19]=='483403245','QEMU generation differs')
        need(sha('/opt/baarcha-cube/storage-guard/observe.py')=='b5e32483aeb77b170cc49cc0c429630f2ba3d3fad880c7c80472e2e278541ed3','installed observer differs')
        need(sha('/etc/baarcha-cube/storage-guard.json')=='9df51e0cf0af1735388506192ba7758214a683c81da2536224f998ffa6bf3bd8','reviewed guard differs')
        spec=importlib.util.spec_from_file_location('observer','/opt/baarcha-cube/storage-guard/observe.py');observer=importlib.util.module_from_spec(spec);spec.loader.exec_module(observer)
        guard=observer.trusted_read(P('/etc/baarcha-cube/storage-guard.json'));observer.validate_config(guard)
        need(guard['expected_boot_id']==BOOT,'worker guard pin differs')
        sample=observer.trusted_read(P(guard['observation_path']))
        need(sample['worker_boot_id']==BOOT and sample['outer_boot_id']==guard['outer_boot_id']==observer.read_outer_boot(),'sample boot differs')
        need(0<=observer.boottime_ns()-sample['started_boottime_ns']<30_000_000_000,'stale observation')
        need(min(sample['inner_free_bytes'],sample['outer_free_bytes'])>=96*1024**3,'storage reserve insufficient')
        need(subprocess.check_output(['systemctl','is-active','baarcha-cube-storage-observer.timer'],text=True).strip()=='active','observer timer inactive')
        frozen=P('/opt/baarcha-bench/cube-host-lifecycle-enrollment-20260925/cube-host-enrollment-execute-check06.py')
        need(sha(frozen)=='ca6dea40bba2e4f3cc15ecfba122b18ad69fc4ece17567f45aac224ce30562da','provider inventory helper changed')
        s=importlib.util.spec_from_file_location('enrollment',frozen);e=importlib.util.module_from_spec(s);s.loader.exec_module(e);e.provider_jobs()
        stop=json.loads(P('/etc/baarcha-cube/worker-stop.json').read_text())
        cfg={'api_url':'http://127.0.0.1:20300','api_key':stop['api_key'],'proxy_url':'http://127.0.0.1:20080','domain':'cube.app','max_active':4,'template_id':'tpl-98b45d63cfcc48c5b6ba9104','work_dir':str(WORK),'storage_guard':guard}
        STAGE.mkdir(mode=0o700);config=STAGE/'config-private.json'
        with config.open('x') as f:json.dump(cfg,f);f.flush();os.fsync(f.fileno())
        group=subprocess.check_output(['systemctl','show','--value','-p','ControlGroup','baarcha-cube-worker-01.service'],text=True).strip()
        need(group=='/system.slice/baarcha-cube-worker-01.service','unexpected worker cgroup')
        root=P('/sys/fs/cgroup'+group)
        def metrics():
            row={'unix_time':time.time()}
            for n in ['memory.current','memory.peak','memory.events','memory.swap.current','memory.pressure','cpu.stat']:
                p=root/n;row[n]=p.read_text() if p.exists() else None
            return row
        before=metrics();samples=[];stop_sample=threading.Event()
        def monitor():
            while not stop_sample.is_set():samples.append(metrics());stop_sample.wait(1)
        thread=threading.Thread(target=monitor,daemon=True);thread.start();code=None
        try:
            runenv=os.environ.copy();runenv['CUBE_WORKLOAD_LIVE_CONFIG']=str(config)
            with (STAGE/'test.log').open('x') as log:
                p=subprocess.run([str(BIN),'-test.run','^TestLiveCubeAppWorkload$','-test.v','-test.timeout','10m15s'],cwd=BASE/'control-plane/internal/store',env=runenv,stdout=log,stderr=subprocess.STDOUT,timeout=650)
                code=p.returncode
        finally:
            stop_sample.set();thread.join(5)
            report={'returncode':code,'before':before,'after':metrics(),'samples':samples,'sampled_peak_bytes':max([int(x['memory.current']) for x in samples if x['memory.current'] is not None] or [0]),'kernel_peak_is_lifetime':True,'cgroup':group,'guard_installed':True,'production_accepted':False}
            (STAGE/'host-cgroup.json').write_text(json.dumps(report,indent=2))
        print(json.dumps({'returncode':code,'stage':str(STAGE)}),flush=True)
        raise SystemExit(code if code is not None else 1)
if __name__=='__main__':main()
