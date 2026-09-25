#!/usr/bin/env python3
"""INERT until root reviews this exact hash and invokes --execute.
Single operator process retains deployment locks across every phase. No forced
shutdown. On mutation failure it holds locks for explicit recovery commands.
"""
import argparse, contextlib, datetime, fcntl, hashlib, importlib.util, json, os
from pathlib import Path
import re, shlex, signal, sqlite3, stat, subprocess, sys, time, urllib.request, fnmatch

BASE=Path('/opt/baarcha-bench/cube-host-lifecycle-enrollment-20260925')
REVIEW=BASE/'review-02'; RELEASE=BASE/'release-be24d2fa8ca1'
WORKER=Path('/opt/baarcha-cube/worker-01'); CONF=Path('/etc/baarcha-cube')
UNIT='baarcha-cube-worker-01.service'; CP='5fb591f9c2705b5611efee74d0fff7c3169897afc7719977c7f2a0d52f25fe67'
IMAGE='sha256:26222f6de55d5923f65f7b48489f2ea3b79b2f21a948e17adff370ca5101c747'
PLATFORM='30cd5c24ff44183fc72f4c051377f8ae931d9863'
INITIAL_BOOT='7ee095fe-0461-4779-93e6-42e2b557440f'; INITIAL_PID=2917754; INITIAL_START='482723086'
MACHINE='2b9e31d4abd345e3bd4b966591e61296'; DATA='793c3349-db9c-4815-9842-989ed484f1f8'
HOST_SHA='94398ad7ebd42af9288c705924e2bbf5c4214561582afeef54d55f32714dbffd'
NESTED_SHA='e1e34848f59f507d0e2ae42b23e2887284248ea0454bd739d827ea716f17e205'
STOP_SHA='ab55a5dda0bfceb6053a1870c7c73413ce2902e53e4c5ea2d2e0f903d20c469f'
START_SHA='e3752688e978540c6dbcee2c7d8786fb24e3d409efaed8a2d880d1bc6c85cfb9'
DB=Path('/var/lib/sandboxd/state/sandboxd.db'); MARKER=Path(str(DB)+'.worker-stop.json')
LOCKS=('/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock',
       '/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock')
TIMERS=tuple('baarcha-'+x+'.timer' for x in ('project-env-apply','classroom-egress','fennec-meet-egress'))
SSH=['/usr/bin/ssh','-i',str(WORKER/'operator-key'),'-p20222','-oBatchMode=yes','-oConnectTimeout=5',
     '-oStrictHostKeyChecking=yes','-oUserKnownHostsFile='+str(WORKER/'known_hosts'),'root@127.0.0.1']
ROUTING=WORKER/'cutover-routing'; SCRIPT='/usr/local/libexec/baarcha-cube-worker-lifecycle.py'
HOLD=Path('/etc/systemd/system/baarcha-cube-worker-01.service.d/90-empty-checkpoint-hold.conf')
ALLOW=Path('/opt/baarcha-bench/cube-durable-enrollment-20260925/allow-worker')
HOLD_SHA='2e67ebf32800b4c734e0d368f0d5ee5875670c505cd62cd19ddb218e1c5fcfe1'
ALLOW_SHA='e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'


def need(ok, why):
    if not ok: raise RuntimeError(why)
def sha(path):
    h=hashlib.sha256()
    with open(path,'rb') as f:
        for b in iter(lambda:f.read(1048576),b''): h.update(b)
    return h.hexdigest()
def utc(): return datetime.datetime.now(datetime.timezone.utc).isoformat()
def private(path, directory=False):
    p=Path(path); s=p.stat()
    need(p.is_absolute() and p.resolve()==p and s.st_uid==0 and stat.S_IMODE(s.st_mode)==(0o700 if directory else 0o600),'unsafe private path')
    need(p.is_dir() if directory else p.is_file(),'wrong private path type'); return p
def atomic(path, value, replace=False):
    path=Path(path); private(path.parent,True)
    raw=(json.dumps(value,sort_keys=True,indent=2)+'\n').encode()
    temp=path.with_name('.'+path.name+'.'+str(os.getpid())+'.tmp')
    fd=os.open(temp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'wb') as f: f.write(raw);f.flush();os.fsync(f.fileno())
    if replace:
        if path.exists(): private(path)
        os.replace(temp,path)
    else: os.link(temp,path);temp.unlink()
    fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY);os.fsync(fd);os.close(fd)
def run(argv, timeout=20):
    p=subprocess.run(argv,capture_output=True,timeout=timeout)
    need(p.returncode==0,'command failed: '+Path(argv[0]).name)
    need(len(p.stdout)<=4*1024*1024,'oversized command result'); return p.stdout

def unit(name):
    raw=run(['systemctl','show',name,'-p','ActiveState','-p','SubState','-p','MainPID','-p','Restart','-p','UnitFileState','-p','FragmentPath','-p','DropInPaths','-p','KillMode','-p','SendSIGKILL','-p','TimeoutStopUSec']).decode()
    return dict(x.split('=',1) for x in raw.splitlines() if '=' in x)
def reviewed_hold(state):
    need(state['UnitFileState']=='disabled' and state['DropInPaths'].split()==[str(HOLD)],'unexpected outer unit overrides/enablement')
    result={}
    for name,path,expected in [('dropin',HOLD,HOLD_SHA),('allow',ALLOW,ALLOW_SHA)]:
        private(path)
        need(sha(path)==expected,'reviewed outer hold content changed')
        info=path.stat();need(info.st_gid==0,'reviewed outer hold group changed')
        result[name]={'path':str(path),'sha256':expected,'uid':info.st_uid,'gid':info.st_gid,'mode':oct(stat.S_IMODE(info.st_mode))}
    return result

def worker(command,timeout=30): return run(SSH+[command],timeout).decode().strip()
def starttime(pid): return Path(f'/proc/{pid}/stat').read_text().rsplit(')',1)[1].split()[19]
def inspect_cp():
    c=json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
    need(c['Id']==CP and c['Image']==IMAGE,'controller identity drift')
    env=dict(x.split('=',1) for x in c['Config']['Env'] if '=' in x)
    need(env.get('SANDBOXD_CUBE_ENABLED','false')=='false','Cube routing no longer disabled')
    return c

def http(url, key=None):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*args,**kwargs): raise RuntimeError('redirect refused')
    request=urllib.request.Request(url,headers={'X-API-Key':key} if key else {})
    with urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect()).open(request,timeout=5) as r:
        data=r.read(1048577)
    need(len(data)<=1048576,'oversized HTTP metadata');return data

def pg_counts():
    code=r'''
const {createRequire}=require('node:module');
const postgres=createRequire('/opt/baarcha/app/landing/package.json')('postgres');
const db=postgres(process.env.DATABASE_URL,{max:1,connect_timeout:3,connection:{statement_timeout:3000}});
(async()=>{try {const result=await db.begin('read only',async q=>({thumbnail:Number((await q`SELECT count(*) AS n FROM project_thumbnail_capture WHERE state='capturing'`)[0].n),env_pending:Number((await q`SELECT count(*) AS n FROM project_env_apply WHERE pending`)[0].n)}));console.log(JSON.stringify(result));}
catch {process.exitCode=1;}finally {await db.end({timeout:3});}})();
'''
    return json.loads(run(['/opt/baarcha/node22/bin/node','--env-file=/opt/baarcha/landing.env','-e',code],12))

def db_counts():
    with contextlib.closing(sqlite3.connect('file:'+str(DB)+'?mode=ro',uri=True,timeout=2)) as c:
        c.execute('PRAGMA query_only=ON');c.execute('BEGIN')
        return {'apps':c.execute('SELECT count(*) FROM app').fetchone()[0],
                'bindings':c.execute('SELECT count(*) FROM runtime_binding').fetchone()[0],
                'admission':c.execute('SELECT count(*) FROM cube_admission').fetchone()[0],
                'recovery':c.execute('SELECT count(*) FROM cube_recovery').fetchone()[0],
                'active':c.execute("SELECT count(*) FROM task WHERE status IN ('running','starting','queued','pending')").fetchone()[0]}

JOB_GROUPS={
 't_cube_rootfs_artifact':{'READY':8}, 't_cube_template_definition':{'READY':8},
 't_cube_template_image_job':{'FAILED':1,'READY':8}, 't_cube_template_replica':{'READY':8},
 't_cube_pause_snapshot':{},'t_cube_snapshot':{},'t_component_import_job':{},'t_component_preinstall_job':{}}

def provider_jobs():
    code = "import json,subprocess\n"+"tables="+repr(sorted(JOB_GROUPS))+"\n"+r'''
command=['docker','exec','-i','cube-sandbox-mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names']
sql="SELECT table_name FROM information_schema.tables WHERE table_schema='cube_mvp' AND table_name IN ("+','.join("'"+t+"'" for t in tables)+") ORDER BY table_name;"
p=subprocess.run(command,input=sql,text=True,capture_output=True,timeout=15)
assert p.returncode==0 and p.stdout.splitlines()==tables, 'provider schema incomplete'
sql='START TRANSACTION READ ONLY;'+''.join("SELECT '"+t+"',status,count(*) FROM cube_mvp."+t+" GROUP BY status;" for t in tables)+'COMMIT;'
p=subprocess.run(command,input=sql,text=True,capture_output=True,timeout=15)
assert p.returncode==0, 'provider grouped state query failed'
out={t:{} for t in tables}
for line in p.stdout.splitlines():
 t,status,count=line.split('\t');assert t in out and status not in out[t];out[t][status]=int(count)
print(json.dumps(out))
'''
    result=subprocess.run(SSH+['python3 -'],input=code.encode(),capture_output=True,timeout=40)
    need(result.returncode==0 and len(result.stdout)<=4096,'provider job enumeration failed')
    value=json.loads(result.stdout);need(value==JOB_GROUPS,'provider job/definition state changed or not terminal')
    return {'at':utc(),'known_terminal_groups':value,'active_jobs':0,'scope':'all eight pinned cube_mvp job/definition tables'}

def container_identities():
    ids=run(['docker','ps','-a','--no-trunc','--format','{{.ID}}']).decode().split()
    need(ids and len(ids)<300,'container inventory bound')
    values=json.loads(run(['docker','inspect',*ids]))
    return sorted([{'id':v['Id'],'image':v['Image'],'name':v['Name']} for v in values],key=lambda v:v['id'])

def orphan_stat():
    path='/data/cubelet/storage/xfs/objects/volumes/tpl-tpl-ce1ee426e686460bbc8c3bfc-build-rootfs/sb-1a1474444e064d6f8da340f324f4fd7f-rootfs-gen0'
    code='import pathlib,json; p=pathlib.Path('+repr(path)+'); assert p.resolve()==p; s=p.stat(); print(json.dumps({"inode":s.st_ino,"bytes":s.st_size,"mtime_ns":s.st_mtime_ns}))'
    value=json.loads(worker('python3 -c '+shlex.quote(code)))
    need(value=={'inode':270239769,'bytes':10737418240,'mtime_ns':1790296419384786773},'retained orphan changed')
    need(worker('findmnt -n -o UUID --target /data')==DATA,'orphan filesystem UUID changed')
    return value

def caddy_fence_scopes(config):
    found=[]
    def visit(value,ancestors):
        if isinstance(value,list):
            for child in value:visit(child,ancestors)
        elif isinstance(value,dict):
            scopes=ancestors+([value['match']] if 'match' in value else [])
            if value.get('handler')=='static_response' and str(value.get('status_code'))=='503':found.append(scopes)
            for k,child in value.items():
                if k!='match':visit(child,scopes)
    visit(config,[])
    return found

def model_routes_unfenced(scopes):
    # Evaluate only the fixed routes' simple matchers; unknown matcher syntax
    # refuses approval rather than silently assuming it cannot affect models.
    def matches(group,path):
        possible=False
        for matcher in group:
            need(set(matcher)<= {'host','path','method'},'unknown 503 matcher grammar')
            host=not matcher.get('host') or any(fnmatch.fnmatchcase('baarcha.tn',x) for x in matcher['host'])
            route=not matcher.get('path') or any(fnmatch.fnmatchcase(path,x) for x in matcher['path'])
            method=not matcher.get('method') or 'POST' in matcher['method'] or (path=='/api/live/transcribe' and 'GET' in matcher['method'])
            possible|=host and route and method
        return possible
    paths=['/api/v1/chat/completions','/api/live/transcribe']
    return all(not any(all(matches(group,path) for group in ancestors) for ancestors in scopes) for path in paths)

def route_status(path,preview=False):
    host='s-00000000000000000000000000-3000.preview.baarcha.tn' if preview else 'baarcha.tn'
    return int(run(['curl','--silent','--show-error','--max-time','10','--resolve',host+':443:127.0.0.1','--output','/dev/null','--write-out','%{http_code}','https://'+host+path],12))

def wait_for(fn,seconds):
    end=time.monotonic()+seconds
    while True:
        if fn(): return
        need(time.monotonic()<end,'bounded observation deadline')
        time.sleep(1)

class Enrollment:
    def __init__(self,job,resize=False):
        self.job=job;self.resize=resize; self.phase='created';self.changed=False;self.worker_touched=False;self.baseline=None
        self.stop=json.loads((REVIEW/'candidates/worker-stop.json').read_text())
        self.life=load_module(RELEASE/'source/ops/cube/worker-lifecycle/lifecycle.py','enrollment_life')
        self.observer=load_module(BASE/'drain_observe.py','enrollment_observer')
    def event(self,name,value): atomic(self.job/(name+'.json'),value)
    def advance(self,phase,value=None):
        self.phase=phase;self.event(phase,{'at':utc(),'phase':phase,**(value or {})})
        atomic(self.job/'status.json',{'phase':phase,'at':utc(),'runner_pid':os.getpid(),'worker_touched':self.worker_touched,'changed':self.changed},True)
        print(json.dumps({'phase':phase,'job':str(self.job)}),flush=True)
    def empty(self):
        c=db_counts();need(c=={'apps':66,'bindings':0,'admission':0,'recovery':0,'active':0},'canonical fleet differs/not empty of Cube')
        api=json.loads(http('http://127.0.0.1:20300/sandboxes',self.stop['api_key']));need(api==[],'provider inventory not empty')
        inv=worker('cubemastercli -a 127.0.0.1 list --all --wide')
        need(re.findall(r'^SANDBOX_COUNT\s+(\d+)\s*$',inv,re.M)==['0'] and re.search(r'^NODES_SCANNED\s+1/1\s*$',inv,re.M),'incomplete provider CLI inventory')
        need(not worker('ctr --address /data/cubelet/cubelet.sock --namespace default tasks list --quiet'),'provider tasks present')
        self.latest_provider_jobs=provider_jobs()
        return c
    def nested_ready(self,old_boot=None):
        boot=worker('cat /proc/sys/kernel/random/boot_id');need(boot!=old_boot,'worker did not reboot')
        need(worker('sha256sum '+SCRIPT).split()[0]==NESTED_SHA,'nested helper drift')
        value=json.loads(worker('python3 '+SCRIPT+' verify-start --machine-id '+MACHINE+' --boot-id '+boot+' --data-uuid '+DATA,90))
        need(value.get('verified') and value.get('boot_id')==boot,'nested readiness refused')
        lcm=json.loads(worker("docker inspect --format '{{json .State}}' cube-lifecycle-manager"))
        need(lcm['Running'] and not lcm['Paused'],'LCM not operational')
        self.empty();orphan_stat();return boot
    def refresh_stop(self):
        status=json.loads(private(WORKER/'lifecycle-status.json').read_text());pid=status['qemu_pid']
        need(status['state']=='running-unreconciled','unexpected supervisor state')
        need(Path(f'/proc/{pid}/exe').resolve()==Path('/usr/bin/qemu-system-x86_64'),'wrong child executable')
        need(Path(f'/proc/{pid}/cmdline').read_bytes().rstrip(b'\0').decode().split('\0')==self.life.fixed_qemu(),'QEMU argv drift')
        self.stop.update(qemu_pid=pid,qemu_start_time=starttime(pid),worker_boot_id=worker('cat /proc/sys/kernel/random/boot_id'),receipt=str(self.job/'pre-drain.json'),evidence_directory=str(self.job/'evidence'))
        atomic(CONF/'worker-stop.json',self.stop,True)
    def preflight(self):
        need(sha(RELEASE/'source/ops/cube/worker-lifecycle/lifecycle.py')==HOST_SHA,'host helper changed')
        need(sha(RELEASE/'binaries/cube-worker-stop')==STOP_SHA and sha(RELEASE/'binaries/cube-worker-start')==START_SHA,'coordinator changed')
        need(run(['git','-C','/opt/baarcha/app/landing','rev-parse','HEAD']).decode().strip()==PLATFORM,'platform deployment changed')
        c=inspect_cp();need(c['State']['Running'] and c['HostConfig']['RestartPolicy']['Name']=='unless-stopped','controller baseline unexpected')
        state=unit(UNIT);need(int(state['MainPID'])==INITIAL_PID and state['Restart']=='no','direct worker changed')
        need(starttime(INITIAL_PID)==INITIAL_START,'direct QEMU generation changed')
        need(Path(f'/proc/{INITIAL_PID}/cmdline').read_bytes().rstrip(b'\0').decode().split('\0')==self.life.fixed_qemu(),'direct QEMU argv differs')
        need(worker('cat /proc/sys/kernel/random/boot_id')==INITIAL_BOOT,'worker boot drift')
        for p in [MARKER,WORKER/'lifecycle-status.json',Path(SCRIPT),CONF/'lifecycle.json']:
            need(not p.exists() and not p.is_symlink(),'first-enrollment artifact exists')
        hold=reviewed_hold(state)
        online=json.loads(http('http://127.0.0.1:2019/config/'))
        adapted=json.loads(run(['caddy','adapt','--config','/etc/caddy/Caddyfile','--adapter','caddyfile']))
        reviewed=json.loads((REVIEW/'caddy-loaded-private.json').read_text())
        need(online==adapted==reviewed,'Caddy online baseline changed')
        for name in ['drain','offline']:
            run(['caddy','validate','--config',str(ROUTING/(name+'.json'))])
        self.baseline={'controller_id':CP,'controller_image':IMAGE,'restart':'unless-stopped','worker_unit':state,'outer_boot_hold':hold,
                       'timers':{t:unit(t) for t in TIMERS},'platform_revision':PLATFORM,'caddy':online,'containers':container_identities(),'retained_orphan':orphan_stat()}
        self.event('baseline',self.baseline)
        unitpath=Path(state['FragmentPath']);need(unitpath==Path('/etc/systemd/system/'+UNIT),'unexpected worker unit path')
        (self.job/'original-worker.service').write_bytes(unitpath.read_bytes());os.chmod(self.job/'original-worker.service',0o600)
        self.nested_ready();need(pg_counts()['thumbnail']==0,'thumbnail capture still pending/live/expired')
        self.advance('preflight-passed',{'canonical':self.empty()})
    def fence(self):
        self.changed=True
        run(['caddy','reload','--config',str(ROUTING/'drain.json')])
        run(['systemctl','stop',*TIMERS])
        wait_for(lambda:all(unit(t.replace('.timer','.service'))['ActiveState']=='inactive' for t in TIMERS),120)
        wait_for(lambda:db_counts()['active']==0 and pg_counts()['thumbnail']==0,120)
        run(['caddy','reload','--config',str(ROUTING/'offline.json')])
        need(json.loads(http('http://127.0.0.1:2019/config/'))==json.loads((ROUTING/'offline.json').read_text()),'offline Caddy not loaded')
        scopes=caddy_fence_scopes(json.loads((ROUTING/'offline.json').read_text()))
        need(scopes and model_routes_unfenced(scopes),'unrelated model/voice route may be fenced')
        checks={path:route_status(path) for path in ['/api/projects','/api/bridge','/api/apps/not-an-app/preview','/api/tools/call']}
        checks['preview_dummy']=route_status('/',True);checks['landing']=route_status('/')
        need(checks['landing']==200 and all(code==503 for path,code in checks.items() if path!='landing'),'actual offline route fence failed')
        self.event('offline-route-checks',{'statuses':checks,'model_routes_checked_in_config_only':True})
        wait_for(lambda: self.observer.process_sockets(inspect_cp()['State']['Pid'])['tcp_non_listen']==0,120)
        before=self.observer.collect(CP);self.event('drain-before-stop',before)
        need(not before['errors'] and before['scheduled_runtime_writers_inactive'] and before['caddy']['equals_reviewed_offline'],'scoped drain observation incomplete')
        need(before['controller_sockets']['stable_socket_set'] and before['controller_sockets']['tcp_non_listen']==0,'controller has active/unresolved TCP')
        need(db_counts()['active']==0 and pg_counts()['thumbnail']==0,'writer appeared before stop')
        self.shutdown_since=utc();run(['docker','update','--restart=no',CP])
        run(['docker','stop','--time=-1',CP],60)
        c=inspect_cp();need(not c['State']['Running'],'controller still running')
        after=self.observer.collect(CP,self.shutdown_since);self.event('drain-after-stop',after)
        need(not after['errors'] and after['controller_shutdown']['regular_http_shutdown_success_observed'],'HTTP shutdown not observed successful')
        self.empty();need(pg_counts()['thumbnail']==0,'thumbnail writer remains')
        self.closed_backup()
        self.advance('controller-fenced',{'scope':'empty-Cube canonical controller/runtime writers; unrelated inference left running'})
    def closed_backup(self):
        checked={}
        for p in [DB,Path(str(DB)+'-wal'),Path(str(DB)+'-shm')]:
            if p.exists():
                st=p.stat();checked[(st.st_dev,st.st_ino)]=True
        for proc in Path('/proc').iterdir():
            if not proc.name.isdigit() or int(proc.name)==os.getpid():continue
            try:
                for fd in (proc/'fd').iterdir():
                    try:st=fd.stat()
                    except (FileNotFoundError,ProcessLookupError):continue
                    need((st.st_dev,st.st_ino) not in checked,'canonical DB still opened by other process')
            except (FileNotFoundError,ProcessLookupError):continue
        target=self.job/'controller-before-worker.sqlite';need(not target.exists(),'DB backup already exists')
        with contextlib.closing(sqlite3.connect('file:'+str(DB)+'?mode=ro',uri=True)) as src, contextlib.closing(sqlite3.connect(target)) as dst:
            src.backup(dst);need(dst.execute('PRAGMA integrity_check').fetchall()==[('ok',)],'DB backup integrity failed')
        os.chmod(target,0o600)
        with target.open('rb') as f:os.fsync(f.fileno())
        self.event('controller-backup',{'database':str(target),'sha256':sha(target),'key_path':'/var/lib/sandboxd/secrets.key','key_sha256':sha('/var/lib/sandboxd/secrets.key'),'image':IMAGE,'container':CP,'independent_full_backup':False})
    def verify_locks(self):
        state=unit(UNIT);need(reviewed_hold(state)==self.baseline['outer_boot_hold'],'outer hold changed after boot')
        supervisor=int(state['MainPID']);qemu=self.stop['qemu_pid'];need(supervisor!=qemu and supervisor>1,'missing supervisor')
        for name in ['supervisor.lock','backup.lock']:
            lock=WORKER/name;st=lock.stat()
            for pid in [supervisor,qemu]:
                inherited=False
                for fd in Path(f'/proc/{pid}/fd').iterdir():
                    try:opened=fd.stat()
                    except FileNotFoundError:continue
                    if (opened.st_dev,opened.st_ino)==(st.st_dev,st.st_ino):inherited=True
                need(inherited,'lifetime lock not inherited by exact process')
            code='import fcntl,os,sys;f=os.open('+repr(str(lock))+',os.O_RDWR);\ntry: fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)\nexcept BlockingIOError: sys.exit(0)\nsys.exit(1)'
            run(['/usr/bin/python3','-c',code])
        self.event('lifetime-locks-'+str(qemu),{'supervisor_pid':supervisor,'qemu_pid':qemu,'both_fds_inherited':True,'independent_exclusive_attempts_refused':True,'unit_file_state':state['UnitFileState']})
    def manual_stop(self):
        self.empty();self.worker_touched=True;self.advance('manual-stop-intent')
        args=' --machine-id '+MACHINE+' --boot-id '+INITIAL_BOOT+' --data-uuid '+DATA
        stopped=json.loads(worker('python3 '+SCRIPT+' shutdown-components'+args,240));need(stopped.get('stopped') and stopped['boot_id']==INITIAL_BOOT,'manual retained stop failed')
        self.event('manual-retained-stop',stopped)
        need(starttime(INITIAL_PID)==INITIAL_START,'QEMU changed before powerdown')
        self.life.qmp_powerdown(WORKER/'qmp.sock')
        wait_for(lambda:not Path(f'/proc/{INITIAL_PID}').exists(),180)
        need(unit(UNIT)['ActiveState']=='inactive' and unit(UNIT)['MainPID']=='0','old unit not inactive')
        self.advance('direct-qemu-exited')
    def resize_disk(self):
        if not self.resize:return
        disk=Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2')
        observed=json.loads((REVIEW/'qemu-and-disks.json').read_text())
        prior=next(x for x in observed['disks'] if x['path']==str(disk))
        info=disk.stat();need(disk.resolve()==disk and info.st_dev==prior['device'] and info.st_ino==prior['inode'],'data disk identity changed')
        need(unit(UNIT)['MainPID']=='0' and unit(UNIT)['ActiveState']=='inactive','resize requires stopped worker')
        for proc in Path('/proc').iterdir():
            if not proc.name.isdigit():continue
            try:
                for fd in (proc/'fd').iterdir():
                    try: opened=fd.stat()
                    except (FileNotFoundError,ProcessLookupError):continue
                    need((opened.st_dev,opened.st_ino)!=(info.st_dev,info.st_ino),'data disk still open')
            except (FileNotFoundError,ProcessLookupError):continue
        before=json.loads(run(['qemu-img','info','--output=json',str(disk)]))
        need(before['format']=='qcow2' and before['virtual-size']==320*1024**3 and not before.get('backing-filename') and not before.get('data-file'),'unexpected resize source')
        run(['qemu-img','check','-f','qcow2',str(disk)],120)
        self.advance('resize-intent',{'from_bytes':320*1024**3,'to_bytes':448*1024**3,'fully_backed_capacity_promised':False})
        run(['qemu-img','resize','-f','qcow2',str(disk),'448G'],60)
        after=json.loads(run(['qemu-img','info','--output=json',str(disk)]));need(after['virtual-size']==448*1024**3,'resize not acknowledged')
        run(['qemu-img','check','-f','qcow2',str(disk)],120)
        self.advance('data-disk-expanded',{'virtual_bytes':after['virtual-size']})
    def install(self):
        need(reviewed_hold(unit(UNIT))==self.baseline['outer_boot_hold'],'outer hold changed before installation')
        need(not MARKER.exists() and not (WORKER/'lifecycle-status.json').exists(),'unexpected startup state')
        if CONF.exists(): private(CONF,True)
        else: CONF.mkdir(mode=0o700)
        for source,dest,mode in [
          (RELEASE/'source/ops/cube/worker-lifecycle/lifecycle.py',Path(SCRIPT),0o755),
          (RELEASE/'source/ops/cube/worker-lifecycle/backup_monitor.py',Path('/usr/local/libexec/backup_monitor.py'),0o644),
          (RELEASE/'binaries/cube-worker-stop',Path('/usr/local/libexec/baarcha-cube-worker-stop'),0o755),
          (RELEASE/'binaries/cube-worker-start',Path('/usr/local/libexec/baarcha-cube-worker-start'),0o755)]:
            need(not dest.exists() and not dest.is_symlink() and dest.parent.resolve()==dest.parent,'installed artifact already exists/unsafe')
            fd=os.open(dest,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode)
            with os.fdopen(fd,'wb') as f: f.write(source.read_bytes());f.flush();os.fsync(f.fileno())
        config=json.loads((REVIEW/'candidates/lifecycle.json').read_text())
        config.update(reviewed=True,drain_integration_reviewed=True,first_boot_empty_reviewed=True)
        atomic(CONF/'lifecycle.json',config)
        # Initial candidate remains unusable for stop until new boot refresh.
        self.stop.update(receipt=str(self.job/'pre-drain.json'),evidence_directory=str(self.job/'evidence'))
        atomic(CONF/'worker-stop.json',self.stop)
        unitpath=Path('/etc/systemd/system/'+UNIT)
        need(unitpath.read_bytes()==(self.job/'original-worker.service').read_bytes(),'old unit changed')
        source=RELEASE/'source/ops/cube/worker-lifecycle/baarcha-cube-worker-01.service'
        run(['install','-o','root','-g','root','-m','0644',str(source),str(unitpath)])
        run(['systemd-analyze','verify',str(unitpath)])
        run(['systemctl','daemon-reload']);state=unit(UNIT)
        need(reviewed_hold(state)==self.baseline['outer_boot_hold'],'outer hold changed during installation')
        need(state['KillMode']=='process' and state['SendSIGKILL']=='no' and state['TimeoutStopUSec']=='infinity' and state['Restart']=='no','unsafe loaded supervisor contract')
        self.advance('host-artifacts-installed')
    def boot(self,previous,label):
        run(['systemctl','start',UNIT]);boot=None
        if self.resize and label=='first-supervisor-ready':
            def ssh_ready():
                try:return worker('cat /proc/sys/kernel/random/boot_id')!=previous
                except (RuntimeError,subprocess.SubprocessError,OSError):return False
            wait_for(ssh_ready,180)
            need(worker('findmnt -n -o SOURCE,FSTYPE,UUID --target /data').split()==['/dev/vdb','xfs',DATA],'XFS whole-device identity differs')
            need(int(worker('blockdev --getsize64 /dev/vdb'))==448*1024**3,'guest block device not expanded')
            worker('xfs_growfs /data',90)
            need(worker('findmnt -n -o UUID --target /data')==DATA,'XFS UUID changed')
            fs=json.loads(worker("python3 -c "+shlex.quote('import os,json;v=os.statvfs("/data");print(json.dumps({"bytes":v.f_blocks*v.f_frsize,"free":v.f_bavail*v.f_frsize}))')))
            need(fs['bytes']>440*1024**3 and fs['free']>=96*1024**3,'expanded XFS capacity/reserve insufficient')
            self.advance('guest-filesystem-expanded',fs)
        def ready():
            nonlocal boot
            try: boot=self.nested_ready(previous);return True
            except (RuntimeError,subprocess.SubprocessError,OSError,ValueError): return False
        wait_for(ready,300);self.refresh_stop();self.verify_locks()
        self.advance(label,{'worker_boot':boot,'qemu_pid':self.stop['qemu_pid']});return boot
    def coordinated_stop(self):
        inventory=json.loads(run(['/usr/local/libexec/baarcha-cube-worker-stop','--config','/etc/baarcha-cube/worker-stop.json','--inventory']))
        need(inventory['Bindings']==[] and re.fullmatch('[a-f0-9]{64}',inventory['SHA256']),'offline inventory not empty')
        self.event('offline-inventory',inventory);self.empty()
        fresh=self.observer.collect(CP,self.shutdown_since)
        need(not fresh['errors'] and fresh['scheduled_runtime_writers_inactive'] and fresh['caddy']['equals_reviewed_offline'],'fresh fence incomplete')
        need(fresh['controller_shutdown']['regular_http_shutdown_success_observed'] and pg_counts()['thumbnail']==0,'scoped writer drain lost')
        fresh['provider_jobs']=self.latest_provider_jobs;fresh['operator_handoff_scope']='exclusive named operator/deploy locks and root-reviewed user handoff; not a kernel sandbox for arbitrary root writers'
        self.event('fresh-scoped-drain',fresh)
        receipt={'version':1,'generated_at':utc(),'controller_id':CP,'worker_boot_id':self.stop['worker_boot_id'],'qemu_pid':self.stop['qemu_pid'],'qemu_start_time':self.stop['qemu_start_time'],'inventory_sha256':inventory['SHA256'],
                 'caddy_configuration_sha256':fresh['caddy']['loaded_sha256'],'evidence_sha256':sha(self.job/'fresh-scoped-drain.json'),
                 'traffic_fenced':True,'existing_requests_drained':True,'direct_writers_fenced':True,'provider_jobs_drained':True}
        atomic(self.job/'pre-drain.json',receipt)
        self.advance('coordinator-stop-intent')
        run(['systemctl','stop','--no-block',UNIT])
        def stopped():
            s=json.loads((WORKER/'lifecycle-status.json').read_text())
            need(s['state'] not in ('stop-blocked','worker-lost'),'supervisor refused/lost worker; retain fence')
            return s['state']=='stopped-clean' and unit(UNIT)['ActiveState']=='inactive' and unit(UNIT)['MainPID']=='0'
        wait_for(stopped,1080)
        need(MARKER.exists(),'coordinator marker absent')
        proofs=list((self.job/'evidence').glob('pause-*.json'));need(len(proofs)==1,'pause proof not unique')
        proof=json.loads(proofs[0].read_text());need(proof['verified'] and proof['guest_states']=={},'actual pause proof wrong')
        clean=[]
        for p in WORKER.glob('clean-stop-*.json'):
            if json.loads(p.read_text()).get('proof')==proof: clean.append(p)
        need(len(clean)==1,'matching immutable clean receipt missing')
        atomic(CONF/'worker-start.json',{'version':1,'pause_proof':str(proofs[0]),'clean_receipt':str(clean[0])})
        config=json.loads((CONF/'lifecycle.json').read_text());config['first_boot_empty_reviewed']=False;atomic(CONF/'lifecycle.json',config,True)
        self.advance('coordinator-stopped-clean',{'pause_proof':str(proofs[0]),'clean_receipt':str(clean[0])})
    def restore_traffic(self):
        need(not MARKER.exists(),'stop marker prevents controller restart')
        run(['docker','update','--restart='+self.baseline['restart'],CP]);run(['docker','start',CP])
        def healthy():
            try: return http('http://127.0.0.1:9090/healthz').strip()==b'ok' and http('http://127.0.0.1:9090/readyz').strip()==b'ready'
            except Exception:return False
        wait_for(healthy,30);inspect_cp()
        online=self.job/'online-caddy.json';atomic(online,self.baseline['caddy'])
        run(['caddy','reload','--config',str(online)])
        need(json.loads(http('http://127.0.0.1:2019/config/'))==self.baseline['caddy'],'Caddy restore mismatch')
        for timer,s in self.baseline['timers'].items():
            if s['ActiveState']=='active':run(['systemctl','start',timer])
        self.advance('traffic-restored')
    def execute(self):
        self.preflight();self.fence();self.manual_stop();self.resize_disk();self.install()
        boot=self.boot(INITIAL_BOOT,'first-supervisor-ready');self.coordinated_stop()
        self.boot(boot,'second-supervisor-ready')
        value=json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start'],180))
        need(value['tenant_ready'] and not MARKER.exists(),'startup reconciliation did not complete')
        self.event('startup-reconciled',value);self.restore_traffic()
        final_containers=container_identities();self.event('container-identity-after',{'containers':final_containers,'unchanged':final_containers==self.baseline['containers']})
        need(final_containers==self.baseline['containers'],'Docker identity changed during enrollment')
        self.event('retained-orphan-after',orphan_stat())
        need(reviewed_hold(unit(UNIT))==self.baseline['outer_boot_hold'],'unit enablement/hold drift')
        self.advance('complete',{'global_cube_enabled':False,'unit_enabled_at_boot':False,'real_coordinator_cycle':True})
    def failed(self,error):
        failure={'at':utc(),'phase':self.phase,'error_class':type(error).__name__,'message':str(error) if isinstance(error,RuntimeError) else 'bounded operation failed','worker_touched':self.worker_touched,'marker_present':MARKER.exists()}
        try:self.event('failure',failure)
        except Exception:print('Failure evidence write failed; retaining locks',flush=True)
        if not self.changed:return
        print(json.dumps({'blocked':True,'locks_retained':True,'job':str(self.job),'recovery':'--request-recovery restore-before-worker OR release-after-manual-recovery'}),flush=True)
        handled=set()
        while True:
            for request in sorted(self.job.glob('recovery-command-*.json')):
                if request.name in handled:continue
                handled.add(request.name)
                try:
                    value=json.loads(private(request).read_text());need(value.get('job')==str(self.job),'recovery job differs')
                    action=value.get('action')
                    if action=='restore-before-worker':
                        need(not self.worker_touched and not MARKER.exists(),'automatic early rollback forbidden after worker/marker mutation')
                        self.restore_traffic();self.advance('aborted-before-worker');return
                    if action=='release-after-manual-recovery':
                        need(value.get('operator_accepts_remaining_fence') is True,'explicit recovery ownership required')
                        self.advance('released-to-manual-recovery',{'marker_present':MARKER.exists(),'automatic_unfence':False});return
                    raise RuntimeError('unknown recovery action')
                except Exception as recovery_error:
                    try:self.event('recovery-refused-'+str(time.time_ns()),{'error_class':type(recovery_error).__name__,'locks_retained':True})
                    except Exception:print('Recovery remains blocked; locks retained',flush=True)
            time.sleep(1)

def load_module(path,name):
    s=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--check-only',action='store_true');p.add_argument('--resize-data-to-448g',action='store_true');p.add_argument('--execute',action='store_true');p.add_argument('--script-sha256',required=True)
    p.add_argument('--job',required=True);p.add_argument('--request-recovery',choices=['restore-before-worker','release-after-manual-recovery'])
    a=p.parse_args();need(os.geteuid()==0 and sys.platform=='linux','native root required')
    need(sha(__file__)==a.script_sha256,'exact reviewed script hash required')
    job=Path(a.job);need(job.parent==BASE and re.fullmatch('execute-[a-z0-9-]+',job.name),'new fixed private execution directory required')
    if a.request_recovery:
        private(job,True);atomic(job/('recovery-command-'+str(time.time_ns())+'.json'),{'job':str(job),'at':utc(),'action':a.request_recovery,'operator_accepts_remaining_fence':a.request_recovery=='release-after-manual-recovery'});return
    need(a.execute != a.check_only,'choose exactly one: --check-only or root-reviewed --execute')
    os.umask(0o077);job.mkdir(mode=0o700);(job/'evidence').mkdir(mode=0o700)
    with contextlib.ExitStack() as stack:
        for name in LOCKS:
            path=Path(name);need(path.resolve(strict=True)==path,'operator lock missing/symlink')
            fd=os.open(path,os.O_RDWR|os.O_NOFOLLOW);st=os.fstat(fd)
            need(stat.S_ISREG(st.st_mode) and st.st_uid==0,'unsafe operator lock')
            fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);stack.callback(os.close,fd)
        for sig in (signal.SIGINT,signal.SIGTERM,signal.SIGHUP):signal.signal(sig,lambda *_:print('Signal noted; recovery protocol retains locks',flush=True))
        task=Enrollment(job,a.resize_data_to_448g)
        try:
            if a.check_only:
                task.preflight();task.advance('check-only-complete',{'service_changes':False,'review_flags_set':False});return
            task.execute()
        except Exception as error:task.failed(error);raise SystemExit(1)

if __name__=='__main__':main()
