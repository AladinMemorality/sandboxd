#!/usr/bin/env python3
"""Prepared benchmark-only runner. Never changes quotas, services or canary data.

The reviewed operator must first pause/checkpoint the retained canary, stop the
exact controller (Cube=false alone does NOT fence existing bindings), freeze
LCM and install the selected test quota. This script verifies those prerequisites.
All fixture guest cleanup stays inside the ownership-scoped Go runner.
"""
import argparse,contextlib,fcntl,hashlib,json,math,os,pathlib,re,sqlite3,stat,subprocess,threading,time
P=pathlib.Path
GIB=1024**3
QUOTAS={4:(10000,'10Gi'),6:(14000,'14Gi'),8:(19000,'18Gi'),12:(28000,'30Gi')}
LOCKS=('/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock')
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oBatchMode=yes','-oConnectTimeout=5','-oStrictHostKeyChecking=yes','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
def need(ok,message):
    if not ok:raise ValueError(message)
def trusted(path,limit=1048576):
    p=P(path);s=p.lstat()
    need(p.is_absolute() and p.resolve()==p and stat.S_ISREG(s.st_mode) and s.st_uid==0 and s.st_nlink==1 and not s.st_mode&0o077 and s.st_size<=limit,'unsafe private input')
    for parent in p.parents:
        st=parent.stat();need(st.st_uid==0 and not st.st_mode&0o022,'unsafe input parent')
    return p.read_bytes()
def sha(raw):return hashlib.sha256(raw).hexdigest()
def read(path):return json.loads(trusted(path))
def command(args,timeout=10):
    p=subprocess.run(args,capture_output=True,timeout=timeout)
    need(p.returncode==0 and len(p.stdout)<1048576,'bounded operator observation failed')
    return p.stdout
def boot():return P('/proc/sys/kernel/random/boot_id').read_text().strip()
def fresh(receipt,config,now):
    need(receipt['version']==1 and receipt['outer_boot_id']==boot(),'receipt boot mismatch')
    need(0<=now-receipt['started_boottime_ns']<=300_000_000_000,'stale drain receipt')
    need(receipt['controller_id']==config['controller_id'] and receipt['worker_boot_id']==config['worker_boot_id'],'receipt identity mismatch')
    need(receipt['controller_stopped'] is True and receipt['canonical_charged']==0 and receipt['canonical_active_tasks']==0 and receipt['provider_active_jobs']==0 and receipt['worker_tasks']==0,'incomplete drain')
    need(receipt['paused_baseline']==config['fixture'].get('paused_baseline'),'baseline receipt mismatch')
    need(receipt['native_quota']==list(QUOTAS[config['fixture']['max_active']]),'unreviewed quota')
def controller_held(config):
    rows=json.loads(command(['docker','inspect',config['controller_id']]))
    need(len(rows)==1 and rows[0]['Id']==config['controller_id'] and rows[0]['Image']==config['controller_image'] and rows[0]['State']['Running'] is False and rows[0]['State']['Restarting'] is False,'controller hold is absent')
def lcm_held(config):
    need(re.fullmatch('[0-9a-f]{64}',config['lcm_id']) is not None,'invalid frozen LCM identity')
    rows=json.loads(command(SSH+['docker inspect '+config['lcm_id']],15))
    need(len(rows)==1 and rows[0]['Id']==config['lcm_id'] and rows[0]['State']['Running'] and rows[0]['State']['Paused'],'LCM hold disappeared')
def worker_limits():
    group=P('/sys/fs/cgroup/system.slice/baarcha-cube-worker-01.service')
    need(int((group/'memory.max').read_text())==44*GIB and int((group/'memory.high').read_text())==42*GIB and int((group/'memory.swap.max').read_text())==0,'worker memory limits changed')
    cpu=(group/'cpu.max').read_text().split();need(cpu[0]!='max' and int(cpu[0])/int(cpu[1])==10,'worker CPU limit changed')
def canonical_drained(config):
    with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=2)) as db:
        db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
        need(db.execute('SELECT COALESCE(SUM(charged),0) FROM cube_admission').fetchone()[0]==0,'canonical charge remains')
        need(db.execute("SELECT count(*) FROM task WHERE status IN ('running','starting','queued','pending')").fetchone()[0]==0,'canonical task remains')
        rows=db.execute('SELECT runtime_id,template_id,sandbox_id FROM runtime_binding').fetchall()
        baseline=config['fixture'].get('paused_baseline')
        if baseline:
            need(len(rows)==1 and rows[0][:2]==(baseline['runtime_id'],baseline['template_id']),'canonical binding mismatch')
            row=db.execute('SELECT app_id,status FROM sandbox WHERE id=?',(rows[0][2],)).fetchone()
            need(row==(baseline['app_id'],'stopped'),'canonical canary not stopped')
        else:need(not rows,'unexpected canonical Cube binding')
def worker_preflight(config):
    # Fixed read-only code; JSON config values are stdin, never shell fragments.
    code=r'''
import json,pathlib,subprocess,sys,yaml
c=json.loads(sys.stdin.readline());p=pathlib.Path
assert p('/proc/sys/kernel/random/boot_id').read_text().strip()==c['worker_boot_id']
quota=yaml.safe_load(p('/usr/local/services/cubetoolbox/Cubelet/dynamicconf/conf.yaml').read_text())['host']['quota']
assert quota=={'mcpu_limit':c['mcpu'],'mem_limit':c['memory'],'mvm_limit':128,'creation_concurrent_num':1,'paused_resource_release_ratio':1.0}
assert subprocess.check_output(['findmnt','-n','-o','UUID','--target','/data'],text=True).strip()=='793c3349-db9c-4815-9842-989ed484f1f8'
tasks=subprocess.check_output(['timeout','5s','ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list'],text=True)
assert len(tasks.strip().splitlines())==1 and 'TASK' in tasks and 'PID' in tasks
lcm=json.loads(subprocess.check_output(['docker','inspect',c['lcm_id']]))[0]
assert lcm['Id']==c['lcm_id'] and lcm['State']['Running'] and lcm['State']['Paused']
tables=['t_cube_rootfs_artifact','t_cube_template_definition','t_cube_template_image_job','t_cube_template_replica','t_cube_pause_snapshot','t_cube_snapshot','t_component_import_job','t_component_preinstall_job']
mysql=['docker','exec','-i','cube-sandbox-mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names']
sql="SELECT table_name FROM information_schema.tables WHERE table_schema='cube_mvp' AND table_name IN ("+','.join("'"+t+"'" for t in tables)+") ORDER BY table_name;"
r=subprocess.run(mysql,input=sql,text=True,capture_output=True,timeout=15);assert r.returncode==0 and r.stdout.splitlines()==sorted(tables)
sql='START TRANSACTION READ ONLY;'+''.join("SELECT '"+t+"',status,count(*) FROM cube_mvp."+t+" GROUP BY status;" for t in tables)+'COMMIT;'
r=subprocess.run(mysql,input=sql,text=True,capture_output=True,timeout=15);assert r.returncode==0
for line in r.stdout.splitlines():
 table,status,count=line.split('\t');assert table in tables and status in ('READY','FAILED') and int(count)>=0
print(json.dumps({'quota':quota,'worker_tasks':0,'provider_active_jobs':0,'lcm_frozen':True}))
'''
    # Use a fixed remote Python launcher, code and parameters over stdin.
    payload={'worker_boot_id':config['worker_boot_id'],'mcpu':QUOTAS[config['fixture']['max_active']][0],'memory':QUOTAS[config['fixture']['max_active']][1],'lcm_id':config['lcm_id']}
    launcher='import sys,json,io; data=json.load(sys.stdin); sys.stdin=io.StringIO(data["input"]); exec(compile(data["code"],"benchmark-preflight","exec"))'
    import shlex
    p=subprocess.run(SSH+['python3 -c '+shlex.quote(launcher)],input=json.dumps({'code':code,'input':json.dumps(payload)+'\n'}).encode(),capture_output=True,timeout=50)
    need(p.returncode==0 and len(p.stdout)<4096,'worker hold/quota/jobs preflight failed')
    return json.loads(p.stdout)
def bounded_unit():
    group=P('/proc/self/cgroup').read_text().strip().split('::')[-1]
    root=P('/sys/fs/cgroup')/group.lstrip('/')
    need(group.startswith('/system.slice/'),'bounded transient unit required')
    cpu=(root/'cpu.max').read_text().split()
    need(cpu[0]!='max' and int(cpu[0])/int(cpu[1])<=2,'runner CPU limit required')
    need(int((root/'memory.max').read_text())<=2*GIB and int((root/'pids.max').read_text())<=128,'runner memory/PID limits required')
def latency_gate(result):
    need(result.get('all_apps_memory_and_cpu_verified') is True and result.get('over_budget_refused') is True and result.get('cleanup_verified') is True and result.get('charged_remaining')==0,'incomplete workload result')
    need(result['preparation_seconds']<=20 and result['measured_load_seconds']>=45,'workload phase duration failed')
    values={}
    for key in ('HTTPMS','StatusMS'):
        samples=sorted(float(row[key]) for batch in result['rounds'][1:] for row in batch)
        need(samples and all(math.isfinite(x) and x>=0 for x in samples),'invalid latency samples')
        values[key]={'p95_ms':samples[math.ceil(.95*len(samples))-1],'max_ms':samples[-1],'samples':len(samples)}
        need(values[key]['p95_ms']<=250 and values[key]['max_ms']<=5000,'steady latency gate failed')
    return values
def main():
    a=argparse.ArgumentParser();a.add_argument('config');a.add_argument('--execute',action='store_true');args=a.parse_args()
    os.umask(0o077);need(os.geteuid()==0,'outer root operator required');config=read(args.config)
    need(config['fixture']['max_active'] in QUOTAS and config['fixture']['storage_guard'] is not None,'reviewed slots and real storage guard required')
    need(sha(trusted(config['binary'],128<<20))==config['binary_sha256'],'binary hash changed')
    receipt=read(config['drain_receipt']);fresh(receipt,config,time.clock_gettime_ns(time.CLOCK_BOOTTIME))
    baseline=config['fixture'].get('paused_baseline')
    if baseline:
        need(sha(trusted(config['marker_receipt']))==baseline['marker_receipt_sha256'],'checkpoint marker receipt changed')
        need(sha(trusted(config['canary_config_receipt']))==baseline['config_sha256'],'checkpoint configuration receipt changed')
    stage=P(config['stage']);work=P(config['fixture']['work_dir'])
    for path in (stage,work):need(str(path).startswith('/opt/baarcha-bench/cube-workload-') and path.resolve()==path and not path.exists(),'unique owned stages required')
    with contextlib.ExitStack() as stack:
        for path in LOCKS:
            fd=os.open(path,os.O_RDWR|os.O_NOFOLLOW);stack.callback(os.close,fd);st=os.fstat(fd);need(st.st_uid==0 and stat.S_ISREG(st.st_mode) and st.st_nlink==1,'unsafe lock');fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
        controller_held(config);canonical_drained(config);worker_limits();worker=worker_preflight(config)
        if not args.execute:print(json.dumps({'preflight':True,'worker':worker,'guest_mutations':False}));return
        bounded_unit();stage.mkdir(mode=0o700)
        fixture=stage/'fixture-private.json';fixture.write_text(json.dumps(config['fixture']))
        stop=threading.Event();failures=[]
        def watch_hold():
            while not stop.wait(1):
                try:controller_held(config);lcm_held(config)
                except Exception:
                    failures.append('controller hold disappeared')
                    if work.exists():(work/'abort-request.json').write_text(json.dumps({'reason':failures[-1]}))
                    return
        thread=threading.Thread(target=watch_hold,daemon=True);thread.start();rc=None
        try:
            env=os.environ.copy();env['CUBE_WORKLOAD_LIVE_CONFIG']=str(fixture)
            with (stage/'test.txt').open('x') as log:
                p=subprocess.run([config['binary'],'-test.run','^TestLiveCubeAppWorkload$','-test.v','-test.timeout','10m15s'],cwd=config['store_source_dir'],env=env,stdout=log,stderr=subprocess.STDOUT,timeout=650);rc=p.returncode
        finally:
            stop.set();thread.join(15)
            (stage/'wrapper-result.json').write_text(json.dumps({'returncode':rc,'hold_failures':failures,'quota_changed':False,'controller_changed':False,'canary_mutated':False,'production_accepted':False},indent=2))
        need(rc==0 and not failures,'benchmark failed; retain evidence and inspect owned cleanup')
        latencies=latency_gate(read(work/'result.json'))
        (stage/'latencies.json').write_text(json.dumps(latencies,indent=2))
        controller_held(config);canonical_drained(config);worker_preflight(config)
        print(json.dumps({'passed':True,'stage':str(stage),'production_accepted':False}))
if __name__=='__main__':main()
