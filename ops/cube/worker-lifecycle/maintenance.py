#!/usr/bin/env python3
"""Current-generation nonempty maintenance: inert until explicit reviewed execute.

Owns the four operation locks continuously. This is an incident recovery caller,
not an unattended host-reboot hook. Failure after fencing retains locks and the
pending journal; no automatic reopen, power retry or identity adoption exists.
"""
import argparse
import copy
import datetime
import importlib.util
import json
import hashlib
import os
from pathlib import Path
import re
import signal
import select
import shlex
import stat
import subprocess
import urllib.request
import contextlib
import http.client
import sqlite3
import time


def load(path,name):
    spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

b=load(Path(__file__).with_name('boot_transition.py') if Path(__file__).name=='maintenance.py' else Path('/usr/local/libexec/baarcha-cube-boot-transition.py'),'maintenance_boot')
x=load(Path(__file__).with_name('external_clean.py') if Path(__file__).name=='maintenance.py' else Path('/usr/local/libexec/baarcha-cube-external-clean.py'),'maintenance_external')
ROOT=b.ROOT
EXPECTED={'controller_id','controller_image','outer_machine_id','outer_boot_id','worker_machine_id','worker_boot_id','data_uuid','qemu_pid','qemu_start_time','supervisor_pid','supervisor_start_time'}
BINDING={'sandbox_id','app_id','runtime_id','template_id','config_revision'}
BINDING_MAP={'sandbox_id':'SandboxID','app_id':'AppID','runtime_id':'RuntimeID','template_id':'TemplateID','config_revision':'ConfigRevision'}
TABLES={'t_cube_rootfs_artifact','t_cube_template_definition','t_cube_template_image_job','t_cube_template_replica','t_cube_pause_snapshot','t_cube_snapshot','t_component_import_job','t_component_preinstall_job'}
# No nonterminal state may be made safe merely by supplying a matching count.
TERMINAL={'READY','FAILED','DELETED','TERMINATED'}
FILES=tuple(dict.fromkeys((*b.PINNED,b.STOP,b.GUARD,b.COMPOSE,b.ACTIVE,
 Path('/usr/local/libexec/baarcha-cube-boot-transition.py'),
 Path('/usr/local/libexec/baarcha-cube-maintenance.py'),
 Path('/usr/local/libexec/baarcha-cube-drain-observe.py'),
 Path('/etc/caddy/Caddyfile'),Path('/opt/baarcha/motion-studio/worker.env'),Path('/opt/baarcha/motion-studio/app/server/index.mjs'),Path('/opt/baarcha/motion-studio/app/server/jobs.mjs'),Path('/opt/baarcha/motion-studio/app/server/store.mjs'),Path('/opt/baarcha/motion-studio/app/server/voice.mjs'),Path('/opt/baarcha/motion-studio/app/server/render.mjs'))))
MOTION_SOURCE_ROOT=Path('/opt/baarcha/motion-studio/app/server')
MOTION_SOURCE_NAMES=frozenset(('index.mjs','jobs.mjs','store.mjs','voice.mjs','render.mjs'))
MOTION_SOURCE_LIMIT=1024*1024
API_PATHS=['/api/projects','/api/projects/*','/api/apps/*/remix','/api/apps/*/preview','/api/chat','/api/tools/call','/api/voice','/api/admin/actions']


def need(ok,message):b.require(ok,message)
def object_keys(value,keys,why):need(isinstance(value,dict) and set(value)==set(keys),why)
def strings(value):return isinstance(value,list) and bool(value) and len(value)==len(set(value)) and all(isinstance(s,str) and s for s in value)

def platform_home_result(host,raw):
    # Only the observed canonical www redirect is allowed; never follow it.
    need(host in ('baarcha.tn','www.baarcha.tn') and len(raw)<=32768,'unexpected platform home probe')
    lines=raw.decode('iso-8859-1').split('\r\n')
    need(len(lines)>=3 and re.fullmatch(r'HTTP/1\.[01] [0-9]{3}(?: .*)?',lines[0]) and lines[-2:]==['',''],'invalid platform home response headers')
    status=int(lines[0].split(' ',2)[1]);locations=[]
    for line in lines[1:-2]:
        need(':' in line and not line.startswith((' ','\t')),'invalid platform home header')
        key,value=line.split(':',1)
        if key.lower()=='location':locations.append(value.strip())
    expected=200 if host=='baarcha.tn' else 308
    need(status==expected and locations==([] if expected==200 else ['https://baarcha.tn/']),'platform home status/redirect differs: '+host)
    return {'host':host,'path':'/','status':status,**({'location':locations[0]} if locations else {})}


def start_pause_process(args,fds,stderr_path):
    # Keep native diagnostics private even if the child exits before its proof.
    fd=os.open(stderr_path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    try:return subprocess.Popen(args,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=fd,pass_fds=fds)
    finally:os.close(fd)


def file_digest(path):
    """Fingerprint five non-executed Motion review inputs in service UID layout.

    Helpers/configs retain the generic root-only contract. This narrow reader
    walks retained directory FDs without following links and checks identity,
    ownership and file generation again after hashing bounded bytes.
    """
    p=Path(path)
    if p.parent!=MOTION_SOURCE_ROOT or p.name not in MOTION_SOURCE_NAMES:
        return b.digest(p)
    need(p.is_absolute(),'absolute Motion review path required')
    fds=[];layout=[]
    def identity(st):return (st.st_dev,st.st_ino,st.st_uid,st.st_gid,st.st_mode)
    def generation(st):return (*identity(st),st.st_nlink,st.st_size,st.st_mtime_ns,st.st_ctime_ns)
    try:
        fd=os.open('/',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW);fds.append(fd)
        current=Path('/')
        for part in p.parts[1:-1]:
            current=current/part
            child=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd);fds.append(child);st=os.fstat(child)
            owners=(0,985) if current in (MOTION_SOURCE_ROOT.parent,MOTION_SOURCE_ROOT) else (0,)
            need(stat.S_ISDIR(st.st_mode) and st.st_uid in owners and not st.st_mode&0o022,'Motion source ancestor ownership/mode differs')
            layout.append((fd,part,child,identity(st)));fd=child
        source=os.open(p.name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd);fds.append(source);before=os.fstat(source)
        need(stat.S_ISREG(before.st_mode) and before.st_uid in (0,985) and not before.st_mode&0o022 and before.st_nlink==1 and before.st_size<=MOTION_SOURCE_LIMIT,'untrusted or oversized Motion review source')
        h=hashlib.sha256();size=0
        while True:
            raw=os.read(source,min(65536,MOTION_SOURCE_LIMIT+1-size))
            if not raw:break
            size+=len(raw);need(size<=MOTION_SOURCE_LIMIT,'Motion review source exceeds bound');h.update(raw)
        need(size==before.st_size and generation(os.fstat(source))==generation(before) and generation(os.stat(p.name,dir_fd=fd,follow_symlinks=False))==generation(before),'Motion source changed while fingerprinting')
        for parent,name,child,expected in layout:
            need(identity(os.fstat(child))==expected and identity(os.stat(name,dir_fd=parent,follow_symlinks=False))==expected,'Motion source directory generation changed')
        return h.hexdigest()
    finally:
        for fd in reversed(fds):os.close(fd)


def validate_plan(p):
    object_keys(p,{'version','kind','expected','files','bindings','routing','motion','provider_terminal_counts'},'maintenance plan schema differs; heavy backup is not supported')
    need(p['version']==1 and p['kind']=='current-generation-external-recovery','explicit incident recovery plan required')
    e=p['expected'];object_keys(e,EXPECTED,'exact old generation required')
    need(b.SHA.fullmatch(e['controller_id']) and re.fullmatch(r'sha256:[a-f0-9]{64}',e['controller_image']),'immutable controller required')
    for key in ('outer_machine_id','worker_machine_id'):need(b.HEX.fullmatch(e[key]) and e[key]!='0'*32,'machine identity missing')
    for key in ('outer_boot_id','worker_boot_id','data_uuid'):need(b.UUID.fullmatch(e[key]),'boot/filesystem identity missing')
    for prefix in ('supervisor','qemu'):
        need(type(e[prefix+'_pid']) is int and e[prefix+'_pid']>1 and re.fullmatch(r'[1-9][0-9]*',e[prefix+'_start_time']),'exact process generation required')
    need(e['qemu_pid']!=e['supervisor_pid'],'supervised QEMU generation required')
    object_keys(p['files'],map(str,FILES),'all installed source/config pins required')
    need(all(b.SHA.fullmatch(v) for v in p['files'].values()),'invalid artifact hash')
    need(isinstance(p['bindings'],list) and 0<len(p['bindings'])<=4,'explicit nonempty reviewed binding set required')
    for v in p['bindings']:
        object_keys(v,BINDING,'complete native binding identity required')
        need(all(isinstance(v[k],str) and v[k] for k in BINDING-{'config_revision'}),'binding text missing')
        need(type(v['config_revision']) is int and v['config_revision']>=0,'binding fingerprint missing')
    for key in ('sandbox_id','runtime_id','app_id'):need(len({v[key] for v in p['bindings']})==len(p['bindings']),'duplicate binding identity')
    r=p['routing'];object_keys(r,{'server','online_sha256','platform_hosts','preview_hosts','motion_hosts','unaffected_hosts','preview_probe_hosts'},'explicit routing scope required')
    need(r['server']=='srv0' and b.SHA.fullmatch(r['online_sha256']),'reviewed loaded HTTPS server required')
    allhosts=[]
    for key in ('platform_hosts','preview_hosts','motion_hosts','unaffected_hosts'):
        need(strings(r[key]),'explicit routing host group required')
        for host in r[key]:need(re.fullmatch(r'(?:\*\.)?[A-Za-z0-9.-]+',host) and not host.startswith('.') and '..' not in host,'invalid host matcher')
        allhosts.extend(h.lower() for h in r[key])
    need(len(allhosts)==len(set(allhosts)),'overlapping exact routing groups')
    for host in r['unaffected_hosts']:
        for scope in r['platform_hosts']+r['preview_hosts']+r['motion_hosts']:
            need(not host_matches(scope,host),'unaffected host intersects maintenance scope')
    need(strings(r['preview_probe_hosts']) and all('*' not in h and any(host_matches(w,h) for w in r['preview_hosts']) for h in r['preview_probe_hosts']),'explicit existing preview probe hosts required')
    m=p['motion'];object_keys(m,{'proxy_id','proxy_image','worker_pid','worker_start_time'},'fixed Motion writer identity required')
    need(b.SHA.fullmatch(m['proxy_id']) and re.fullmatch(r'sha256:[a-f0-9]{64}',m['proxy_image']) and type(m['worker_pid']) is int and m['worker_pid']>1 and re.fullmatch(r'[1-9][0-9]*',m['worker_start_time']),'invalid Motion writer identity')
    object_keys(p['provider_terminal_counts'],TABLES,'complete provider table baseline required')
    for counts in p['provider_terminal_counts'].values():
        need(isinstance(counts,dict) and set(counts)<=TERMINAL and all(type(n)is int and n>0 for n in counts.values()),'nonterminal/unrecognized provider state cannot be approved')


def host_matches(pattern,host):
    pattern=pattern.lower();host=host.lower()
    return host==pattern or (pattern.startswith('*.') and host.endswith(pattern[1:]) and host.count('.')==pattern.count('.'))

def routing_variants(online,scope):
    need(b.sha(json.dumps(online,sort_keys=True,separators=(',',':')).encode())==scope['online_sha256'],'actual loaded Caddy configuration changed')
    original=online['apps']['http']['servers'][scope['server']]['routes']
    need(isinstance(original,list) and original,'loaded route set missing')
    def make(offline):
        value=copy.deepcopy(online);rules=[]
        # Prepend ahead of exact @id Motion aliases as well as wildcard previews.
        for hosts,paths in ((scope['motion_hosts'],None),(scope['preview_hosts'],None),(scope['platform_hosts'],API_PATHS)):
            match={'host':hosts}
            if paths is not None:match['path']=paths+(['/api/bridge'] if offline else [])
            # Drain permits existing GET views/stream completion; offline closes
            # those too before controller stop. New preview wakes are always off.
            if not offline and paths is not None:match['method']=['POST','PUT','PATCH','DELETE']
            rules.append({'match':[match],'handle':[{'handler':'static_response','status_code':503,'headers':{'Retry-After':['60'],'Cache-Control':['no-store'],'Content-Type':['application/json']},'body':'{"code":"maintenance","error":"Project maintenance. Please retry shortly."}'}],'terminal':True})
        value['apps']['http']['servers'][scope['server']]['routes']=rules+copy.deepcopy(original)
        return value
    return {'online':copy.deepcopy(online),'drain':make(False),'offline':make(True)}


def verify_bindings(actual,expected):
    need(isinstance(actual,list),'native binding list required')
    projected=[{k:v[source] for k,source in BINDING_MAP.items()} for v in actual]
    need(sorted(projected,key=lambda v:v['sandbox_id'])==sorted(expected,key=lambda v:v['sandbox_id']),'canonical binding/config identity changed')


def verify_counts(actual,expected):
    need(actual==expected,'provider state changed or pending work remains')
    return {'active_jobs':0,'known_terminal_groups':actual,'scope':'all eight reviewed cube_mvp job/definition tables'}


def motion_environment_key(raw):
    """Reviewed single-line EnvironmentFile token subset; never shell evaluation."""
    need(len(raw)<=65536,'Motion environment file exceeds bound')
    values=[line.strip(b' \t').split(b'=',1)[1].strip(b' \t') for line in raw.splitlines() if line.strip(b' \t').startswith(b'STUDIO_WORKER_KEY=')]
    need(len(values)==1 and bool(values[0]),'fixed Motion credential unavailable')
    key=values[0]
    if key[:1] in (b"'",b'"'):
        need(len(key)>=2 and key[-1:]==key[:1],'unterminated Motion credential quote')
        key=key[1:-1]
    # General systemd escapes/continuations are deliberately unsupported. The
    # installed key is one printable token, optionally wrapped in matching quotes.
    need(bool(key) and len(key)<=8192 and all(33<=c<=126 for c in key) and not any(c in key for c in (b"'",b'"',b'\\')),'unsupported Motion credential format')
    return key


def motion_job_summary(value):
    object_keys(value,{'projects'},'Motion projects response schema differs')
    need(isinstance(value['projects'],list) and len(value['projects'])<=10000,'Motion project observation bound exceeded')
    jobs=0
    for project in value['projects']:
        need(isinstance(project,dict) and isinstance(project.get('jobs'),list),'Motion project jobs missing')
        for job in project['jobs']:
            need(isinstance(job,dict) and job.get('status') in ('ready','failed','cancelled','interrupted'),'Motion job is pending or unrecognized')
            jobs+=1;need(jobs<=100000,'Motion job observation bound exceeded')
    return {'projects':len(value['projects']),'terminal_jobs':jobs,'active_jobs':0,'projects_sha256':b.sha(b.encoded(value))}


class Sequence:
    """Real host adapter executes each side effect; persist intent first.

    No phase is blindly retried after an uncertain side effect. A parent retains
    its operation locks while reviewing the pending phase and evidence.
    """
    def __init__(self,host,event):self.host=host;self.event=event
    def stop(self):
        h=self.host
        h.preflight();self.event('preflight-passed')
        self.event('drain-intent');h.drain()
        self.event('drained');h.prepare_stop_config();h.capture_before()
        self.event('before-role-closure');h.fence()
        self.event('pause-intent');pause=h.pause()
        self.event('paused',pause);h.fence()
        self.event('retained-stop-intent');retained=h.retained_stop()
        self.event('management-stopped',retained);h.fence()
        self.event('external-power-intent');receipt=h.external_stop(pause,retained)
        self.event('externally-stopped',receipt)
        return receipt
    def start(self,receipt):
        h=self.host;h.stopped_fence(receipt)
        self.event('start-authorization-intent');h.authorize_start(receipt)
        self.event('start-intent');h.start_worker()
        self.event('new-worker-unreconciled');result=h.transition()
        need(result.get('tenant_ready') is True and result.get('routing_changed') is False,'new generation reconciliation incomplete')
        self.event('new-generation-ready',result);h.capture_after()
        self.event('after-role-closure');h.ready_fence()
        self.event('reopen-intent');h.reopen()
        self.event('complete',result)
        return result


class Host:
    def __init__(self,plan,directory,fds,event):
        self.plan=plan;self.e=plan['expected'];self.job=Path(directory);self.fds=tuple(fds);self.event=event;self.children=[];self.want_stop=None;self.before_stop=None;self.motion_baseline=None
        self.observer=b.load_module('/usr/local/libexec/baarcha-cube-drain-observe.py')
        self.life=b.load_module(b.LIFECYCLE)
        self.bridge=b.Host({});self.nested_context=None
    def command(self,args,timeout=30):
        result=subprocess.run(args,capture_output=True,timeout=timeout,pass_fds=self.fds)
        need(result.returncode==0 and len(result.stdout)<=4*1024*1024,'bounded maintenance command failed: '+Path(args[0]).name)
        return result.stdout
    def inspect(self,ident):return b.strict(self.command(['/usr/bin/docker','inspect',ident]))[0]
    def cp(self,stopped=False):
        value=self.inspect('src-sandboxd-1');need(value['Id']==self.e['controller_id'] and value['Image']==self.e['controller_image'],'controller generation changed')
        if stopped:need(not value['State']['Running'] and not value['State']['Restarting'] and not value['State']['Paused'] and value['State']['Pid']==0 and value['HostConfig']['RestartPolicy']['Name']=='no','controller not authoritatively stopped')
        return value
    def worker(self,code,timeout=40):
        result=subprocess.run(b.SSH+['/usr/bin/python3 -'],input=code.encode(),capture_output=True,timeout=timeout,pass_fds=self.fds)
        need(result.returncode==0 and len(result.stdout)<=65536,'worker read-only proof unavailable');return b.strict(result.stdout)
    def same(self):
        need(Path('/etc/machine-id').read_text().strip()==self.e['outer_machine_id'] and Path('/proc/sys/kernel/random/boot_id').read_text().strip()==self.e['outer_boot_id'],'outer generation changed')
        for prefix in ('qemu','supervisor'):need(x.ticks(self.e[prefix+'_pid'])==self.e[prefix+'_start_time'],'original process generation changed')
        need(Path('/proc/'+str(self.e['qemu_pid'])+'/cmdline').read_bytes().split(b'\0')[:-1]==[v.encode() for v in self.life.fixed_qemu()],'original QEMU launch changed')
        status=b.strict(b.trusted(ROOT/'lifecycle-status.json'));need(status['state']=='stop-blocked' and status['qemu_pid']==self.e['qemu_pid'] and status['supervisor_pid']==self.e['supervisor_pid'],'explicit stop-blocked source required')
        need(self.command(['/usr/bin/systemctl','show','baarcha-cube-worker-01.service','-p','Job','--value']).strip() in (b'',b'0'),'queued worker operation exists')
    def provider(self):
        code='import json,subprocess\ntables='+repr(sorted(TABLES))+'\n'+'''command=['docker','exec','-i','cube-sandbox-mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names']
sql="SELECT table_name FROM information_schema.tables WHERE table_schema='cube_mvp' AND table_name IN ("+','.join("'"+t+"'" for t in tables)+") ORDER BY table_name;"
p=subprocess.run(command,input=sql,text=True,capture_output=True,timeout=15)
assert p.returncode==0 and p.stdout.splitlines()==tables
sql='START TRANSACTION READ ONLY;'+''.join("SELECT '"+t+"',status,count(*) FROM cube_mvp."+t+" GROUP BY status;" for t in tables)+'COMMIT;'
p=subprocess.run(command,input=sql,text=True,capture_output=True,timeout=15);assert p.returncode==0
out={t:{} for t in tables}
for line in p.stdout.splitlines():
 t,status,n=line.split('\\t');assert t in out and status not in out[t];out[t][status]=int(n)
print(json.dumps(out))
'''
        return verify_counts(self.worker(code),self.plan['provider_terminal_counts'])
    def pg_counts(self):
        code="""const {createRequire}=require('node:module');const postgres=createRequire('/opt/baarcha/app/landing/package.json')('postgres');const db=postgres(process.env.DATABASE_URL,{max:1,connect_timeout:3,connection:{statement_timeout:3000}});(async()=>{try{const r=await db.begin('read only',async q=>({thumbnail:Number((await q`SELECT count(*) n FROM project_thumbnail_capture WHERE state='capturing'`)[0].n),env_pending:Number((await q`SELECT count(*) n FROM project_env_apply WHERE pending`)[0].n)}));console.log(JSON.stringify(r))}catch{process.exitCode=1}finally{await db.end({timeout:3})}})();"""
        return b.strict(self.command(['/opt/baarcha/node22/bin/node','--env-file=/opt/baarcha/landing.env','-e',code],12))
    def quiet_tasks(self,permit_busy=False):
        counts=self.observer.sqlite_observation();pg=self.pg_counts();quiet=counts['active_tasks']==0 and pg['thumbnail']==0 and pg['env_pending']==0
        need(quiet or permit_busy,'task/thumbnail/environment work remains');return {'runtime':counts,'platform':pg,'quiet':quiet}
    def bindings_readonly(self):
        with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=2)) as db:
            db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
            rows=db.execute("SELECT b.sandbox_id,s.app_id,b.runtime_id,b.template_id,b.config_revision FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id WHERE b.provider='cube' AND s.runtime_provider='cube' ORDER BY b.sandbox_id").fetchall()
            value=[dict(zip(('sandbox_id','app_id','runtime_id','template_id','config_revision'),row)) for row in rows]
        need(value==sorted(self.plan['bindings'],key=lambda v:v['sandbox_id']),'current canonical bindings differ from reviewed set')
        return value
    def unit(self,name):return self.observer.unit(name)
    def wait(self,check,seconds=180):
        end=time.monotonic()+seconds
        while True:
            try:return check()
            except (OSError,ValueError,RuntimeError,subprocess.SubprocessError):
                if time.monotonic()>=end:raise
                time.sleep(1)
    def preflight(self,defer_busy=False):
        validate_plan(self.plan)
        self.platform_homes() # Check real homepage behavior before any routing fence.
        for path,hash_value in self.plan['files'].items():need(file_digest(path)==hash_value,'reviewed file changed')
        self.same();cp=self.cp();need(cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name']=='unless-stopped','unexpected initial controller state')
        stop=b.strict(b.trusted(b.STOP))
        for k in ('controller_id','worker_machine_id','worker_boot_id','data_uuid','qemu_pid','qemu_start_time'):need(stop[k]==self.e[k],'stop config stale before recovery')
        need(not Path(stop['database']+'.worker-stop.json').exists(),'existing canonical stop marker needs its own continuation')
        active=self.command(['/usr/bin/systemctl','list-units','--plain','--no-legend','--state=activating,active','cube-coding-profile-*']).strip();need(not active,'coding profile remains active')
        proxy=self.inspect(self.plan['motion']['proxy_id']);need(proxy['Image']==self.plan['motion']['proxy_image'] and not proxy['State']['Paused'] and not proxy['State']['Restarting'] and not proxy['State']['OOMKilled'] and (proxy['State']['Running'] or (proxy['State']['Pid']==0 and proxy['State']['Status']=='exited' and proxy['State']['ExitCode']==0)),'Motion proxy changed or not cleanly stopped')
        m=self.plan['motion'];need(x.ticks(m['worker_pid'])==m['worker_start_time'] and self.unit('baarcha-motion-worker.service')['MainPID']==str(m['worker_pid']),'Motion worker changed')
        online=b.strict(b.http('/config/',2019));self.routes=routing_variants(online,self.plan['routing'])
        self.baseline={'controller_restart':cp['HostConfig']['RestartPolicy'],'motion_restart':proxy['HostConfig']['RestartPolicy'],'motion_running':proxy['State']['Running'],'timers':{t:self.unit(t)['ActiveState'] for t in (*b.TIMERS,'baarcha-motion-access.timer')}}
        for name,value in self.routes.items():x.publish(self.job/(name+'.json'),value)
        x.publish(self.job/'baseline.json',self.baseline)
        self.nested_context=self.bridge.worker_lock();self.nested_context.__enter__()
        ids=self.worker("import json,pathlib,subprocess;print(json.dumps({'machine':pathlib.Path('/etc/machine-id').read_text().strip(),'boot':pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'data':subprocess.check_output(['findmnt','-n','-o','UUID','--target','/data'],text=True).strip()}))")
        need(ids=={'machine':self.e['worker_machine_id'],'boot':self.e['worker_boot_id'],'data':self.e['data_uuid']},'nested generation changed')
        self.provider();work=self.quiet_tasks(permit_busy=defer_busy);self.motion_jobs();self.bindings_readonly()
        return {'version':1,'ready':work['quiet'],'deferred':not work['quiet'],'work':work,'bindings':len(self.plan['bindings'])}
    def reload(self,name):
        self.command(['/usr/bin/caddy','validate','--config',str(self.job/(name+'.json'))])
        self.command(['/usr/bin/caddy','reload','--config',str(self.job/(name+'.json'))])
        need(b.strict(b.http('/config/',2019))==self.routes[name],'actual loaded routing differs')
    def platform_homes(self):
        results=[]
        for host in self.plan['routing']['platform_hosts']:
            raw=self.command(['/usr/bin/curl','--silent','--show-error','--http1.1','--noproxy','*','--max-time','5','--resolve',host+':443:127.0.0.1','--output','/dev/null','--dump-header','-','https://'+host+'/'],7)
            results.append(platform_home_result(host,raw))
        return results
    def route_checks(self):
        r=self.plan['routing'];checks=[]
        for host in r['platform_hosts']:
            checks.extend((host,p,503) for p in ('/api/projects','/api/bridge','/api/apps/not-an-app/preview','/api/tools/call'))
        for host in r['motion_hosts']:checks.append((host,'/',503))
        # Every actual reviewed binding supplies an exact existing preview host;
        # wildcard names are never DNS/probe targets.
        for host in r['preview_probe_hosts']:checks.append((host,'/',503))
        for host,path,expected in checks:
            result=self.command(['/usr/bin/curl','--silent','--show-error','--noproxy','*','--max-time','5','--resolve',host+':443:127.0.0.1','--output','/dev/null','--write-out','%{http_code}','https://'+host+path],7)
            need(result==str(expected).encode(),'actual scoped route probe failed')
        return [{'host':h,'path':p,'status':s} for h,p,s in checks]+self.platform_homes()
    def motion_jobs(self):
        path=Path('/opt/baarcha/motion-studio/worker.env')
        raw=b.trusted(path);need(b.sha(raw)==self.plan['files'][str(path)],'Motion credential configuration changed')
        key=motion_environment_key(raw)
        m=self.plan['motion'];need(x.ticks(m['worker_pid'])==m['worker_start_time'],'Motion backend process changed')
        # Only compare selected values privately; never log/store process env.
        env=dict(line.split(b'=',1) for line in Path('/proc/'+str(m['worker_pid'])+'/environ').read_bytes().split(b'\0') if b'=' in line)
        need(not env.get(b'STUDIO_WORKER_URL') and not env.get(b'STUDIO_WORKER_SOCKET') and env.get(b'STUDIO_WORKER_KEY')==key,'Motion worker is a forwarding proxy or credential generation differs')
        conn=http.client.HTTPConnection('172.19.0.1',8332,timeout=5)
        try:
            conn.request('GET','/api/projects',headers={'Authorization':'Bearer '+key.decode(),'Connection':'close'})
            response=conn.getresponse();body=response.read(8*1024*1024+1)
            need(response.status==200 and len(body)<=8*1024*1024,'bounded authenticated Motion job observation unavailable')
            return motion_job_summary(b.strict(body))
        finally:conn.close()
    def scheduled_writers_inactive(self):
        for stem in (*self.observer.WRITERS,'baarcha-motion-access'):
            for suffix in ('.timer','.service'):
                u=self.unit(stem+suffix)
                need(u['LoadState']=='loaded' and u['ActiveState']=='inactive' and (suffix=='.timer' or u['MainPID']=='0'),'scheduled writer not inactive: '+stem+suffix)
    def controller_requests_drained(self):
        cp=self.cp();pid=cp['State']['Pid'];s=self.observer.process_sockets(pid)
        # HTTP Shutdown does not wait for hijacked preview WebSockets. While
        # the listener is still open, their accepted sockets retain its local
        # port. Idle outbound provider pools are not incoming requests.
        need(s['pid']==pid and s['process_generation']==x.ticks(pid) and s['stable_socket_set'] and s['tcp_states'].get('listen',0)>0 and s['tcp_connections_on_listening_ports']==0,'controller incoming connections remain or listener observation changed')
        return s
    def writers(self):
        for name in MOTION_SOURCE_NAMES:
            path=MOTION_SOURCE_ROOT/name;need(file_digest(path)==self.plan['files'][str(path)],'reviewed Motion mutation scope source changed')
        self.scheduled_writers_inactive()
        proxy=self.inspect(self.plan['motion']['proxy_id']);need(proxy['Image']==self.plan['motion']['proxy_image'] and not proxy['State']['Running'] and not proxy['State']['Restarting'] and not proxy['State']['Paused'] and not proxy['State']['OOMKilled'] and proxy['State']['ExitCode']==0 and proxy['HostConfig']['RestartPolicy']['Name']=='no','Motion proxy not cleanly stopped')
        m=self.plan['motion'];sockets=self.observer.process_sockets(m['worker_pid']);need(sockets['process_generation']==m['worker_start_time'] and sockets['stable_socket_set'] and sockets['tcp_non_listen']==0,'Motion backend connections remain')
        jobs=self.motion_jobs()
        if self.motion_baseline is not None:need(jobs==self.motion_baseline,'Motion job/project state changed while fenced')
        return {'sockets':sockets,'jobs':jobs,'motion_host_library_quiescent':False,'scope':'Cube guest/worker disks and canonical runtime writers only; disconnected Motion upload/delete-voice tails may continue host-library writes and fixed external voice deletion; reviewed code has no Cube/controller mutation path'}
    def drain(self):
        self.reload('drain');self.command(['/usr/bin/systemctl','stop',*b.TIMERS,'baarcha-motion-access.timer'])
        def idle():
            for stem in (*self.observer.WRITERS,'baarcha-motion-access'):
                need(self.unit(stem+'.service')['ActiveState']=='inactive','writer service still active')
            return self.quiet_tasks()
        self.wait(idle,1320);self.reload('offline')
        # Save actual current offline at the fixed reader path, with old bytes
        # retained. Parent installation pins this intentional routing CAS.
        x.publish(self.job/'old-offline.json',b.trusted(b.OFFLINE));b.atomic(b.OFFLINE,b.encoded(self.routes['offline']))
        checks=self.route_checks();x.publish(self.job/'route-checks.json',checks)
        self.stop_motion_proxy()
        self.wait(self.writers)
        incoming=self.wait(self.controller_requests_drained);x.publish(self.job/'controller-incoming-drained.json',incoming);self.quiet_tasks();self.provider()
        since=datetime.datetime.now(datetime.timezone.utc).isoformat();self.command(['/usr/bin/docker','update','--restart=no',self.e['controller_id']]);self.command(['/usr/bin/docker','stop','--time=-1',self.e['controller_id']],180)
        cp=self.cp(True);shutdown=self.observer.shutdown_observation(cp,since);need(shutdown['regular_http_shutdown_success_observed'],'controller normal shutdown not proved');x.publish(self.job/'controller-shutdown.json',shutdown)
        final={'tasks':self.quiet_tasks(),'provider':self.provider(),'bindings':self.bindings_readonly()}
        x.publish(self.job/'controller-after-stop.json',final)
        self.motion_baseline=self.motion_jobs();self.fence()
    def stop_motion_proxy(self):
        proxy=self.inspect(self.plan['motion']['proxy_id']);need(proxy['Image']==self.plan['motion']['proxy_image'],'Motion proxy image changed')
        if not self.baseline['motion_running']:need(not proxy['State']['Running'],'originally stopped Motion proxy unexpectedly woke')
        if proxy['HostConfig']['RestartPolicy']['Name']!='no':self.command(['/usr/bin/docker','update','--restart=no',proxy['Id']])
        if proxy['State']['Running']:self.command(['/usr/bin/docker','stop','--time=-1',proxy['Id']],180)
    def fence(self):
        self.same();self.cp(True);self.writers()
        need(b.strict(b.http('/config/',2019))==self.routes['offline'],'offline routing changed')
        need(self.bridge.nested is not None and self.bridge.nested.poll() is None,'nested operator lock lost')
        with b.locked(list(self.fds)):pass
    def prepare_stop_config(self):
        raw=b.trusted(b.STOP);need(b.sha(raw)==self.plan['files'][str(b.STOP)],'STOP changed before journaled CAS')
        old=b.strict(raw);wanted=copy.deepcopy(old);wanted.update(receipt=str(self.job/'pre-drain.json'),evidence_directory=str(self.job/'native-evidence'))
        (self.job/'native-evidence').mkdir(mode=0o700);x.publish(self.job/'stop-before.json',raw);x.publish(self.job/'stop-wanted.json',wanted)
        b.atomic(b.STOP,b.encoded(wanted));self.before_stop=raw;self.want_stop=wanted
    def capture_before(self):
        self.event('backup-not-included',{'full_backup':False,'closed_roles':False})
    def pause(self):
        self.fence();self.quiet_tasks();jobs=self.provider()
        inventory=b.strict(self.command(['/usr/local/libexec/baarcha-cube-worker-stop','--config',str(b.STOP),'--inventory']))
        verify_bindings(inventory['Bindings'],self.plan['bindings'])
        evidence={'version':1,'observed_at':time.time(),'routes':self.route_checks(),'controller_shutdown':b.strict(b.trusted(self.job/'controller-shutdown.json')),'direct_writers':self.writers(),'tasks':self.quiet_tasks(),'provider':jobs,'inventory':inventory}
        x.publish(self.job/'pre-drain-evidence.json',evidence)
        receipt={k:self.e[k] for k in ('controller_id','worker_boot_id','qemu_pid','qemu_start_time')}
        receipt.update(version=1,generated_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),inventory_sha256=inventory['SHA256'],caddy_configuration_sha256=b.sha(b.encoded(self.routes['offline'])),evidence_sha256=b.sha(b.encoded(evidence)),traffic_fenced=True,existing_requests_drained=True,direct_writers_fenced=True,provider_jobs_drained=True)
        x.publish(self.job/'pre-drain.json',receipt)
        process=start_pause_process(['/usr/local/libexec/baarcha-cube-worker-stop','--config',str(b.STOP)],self.fds,self.job/'native-pause.stderr')
        self.children.append(process);end=time.monotonic()+600;raw=b''
        while time.monotonic()<end:
            ready,_,_=select.select([process.stdout],[],[],1)
            if not ready:continue
            chunk=os.read(process.stdout.fileno(),65537-len(raw));need(chunk,'coordinator exited without proof');raw+=chunk;need(len(raw)<=65536,'stop proof oversized')
            if b'\n' in raw:
                line,extra=raw.split(b'\n',1);need(not extra.strip(),'extra coordinator output');proof=b.strict(line);self.life.validate_stop_proof(self.e,proof)
                need(proof['inventory_sha256']==inventory['SHA256'] and proof['guest_states']=={v['runtime_id']:'paused' for v in self.plan['bindings']},'pause proof binding set differs')
                x.publish(self.job/'pause.json',proof);return proof
        raise b.Refused('coordinator pending; retain child, all locks and routing fence')
    def retained_stop(self):
        args=['--machine-id',self.e['worker_machine_id'],'--boot-id',self.e['worker_boot_id'],'--data-uuid',self.e['data_uuid']]
        value=b.strict(self.command(b.SSH+[' '.join(map(shlex.quote,['/usr/bin/python3',str(b.LIFECYCLE),'shutdown-components',*args]))],300))
        need(value.get('stopped') is True and value.get('boot_id')==self.e['worker_boot_id'],'retained shutdown not acknowledged');x.publish(self.job/'retained-stop.json',value);return value
    def external_stop(self,pause,retained):
        identity={k:self.e[k] for k in ('outer_boot_id','supervisor_pid','supervisor_start_time','qemu_pid','qemu_start_time','worker_boot_id','worker_machine_id','data_uuid')};identity.update(version=1,method=x.METHOD,qmp_socket_inode=(ROOT/'qmp.sock').lstat().st_ino)
        d=self.job/'external';d.mkdir(mode=0o700);value=x.shutdown(identity,pause,retained,d,self.fence)
        self.nested_context.__exit__(None,None,None);self.nested_context=None
        for child in self.children:need(child.wait(timeout=15)==0,'held native coordinator failed after QEMU exit')
        return value
    def stopped_fence(self,receipt):
        value=x.validate_receipt(self.job/'external/receipt.json');need(value['receipt']==receipt,'closed external evidence changed')
        self.cp(True);self.writers();need(b.strict(b.http('/config/',2019))==self.routes['offline'],'offline routing lost')
        for key in ('qemu_pid','supervisor_pid'):need(not Path('/proc/'+str(self.e[key])).exists(),'old worker generation still exists')
        need(Path('/proc/sys/kernel/random/boot_id').read_text().strip()==self.e['outer_boot_id'],'outer reboot is not this recovery operation')
        need(b.strict(b.trusted(b.STOP))==self.want_stop,'STOP changed after pause')
        need(b.strict(b.trusted(ROOT/'lifecycle-status.json'))==b.strict(b.trusted(self.job/'external/supervisor-actual.json')),'actual old supervisor record changed')
        with b.locked(list(self.fds)):pass
    def authorize_start(self,receipt):
        self.stopped_fence(receipt)
        need(b.digest(b.EXTERNAL)==self.plan['files'][str(b.EXTERNAL)],'external verifier changed')
        start={'version':1,'pause_proof':str(self.job/'pause.json'),'clean_receipt':str(self.job/'external/receipt.json'),'external_verifier_sha256':b.digest(b.EXTERNAL)}
        need(b.sha(b.trusted(b.START))==self.plan['files'][str(b.START)],'START changed before CAS')
        x.publish(self.job/'start-before.json',b.trusted(b.START));x.publish(self.job/'start-wanted.json',start);b.atomic(b.START,b.encoded(start))
        disks={}
        for p in (ROOT/'root.qcow2',Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2'),ROOT/'seed.img'):
            need(p.resolve(strict=True)==p,'disk identity path changed');s=p.stat();need(stat.S_ISREG(s.st_mode) and s.st_uid==0,'untrusted retained disk')
            disks[str(p)]={'device':s.st_dev,'inode':s.st_ino,'size':s.st_size,'mtime_ns':s.st_mtime_ns}
        auth={'version':1,'purpose':'external-clean-one-use-start','receipt':str(self.job/'external/receipt.json'),'receipt_sha256':b.sha(b.trusted(self.job/'external/receipt.json')),'actual_status_sha256':b.sha(b.trusted(ROOT/'lifecycle-status.json')),'stop_config_sha256':b.sha(b.trusted(b.STOP)),'verifier_sha256':b.digest(b.EXTERNAL),'disk_identities':disks}
        x.publish(x.START_AUTH,auth);x.validate_start_authorization()
        plan={'version':1,'outer_machine_id':self.e['outer_machine_id'],'files':{str(p):b.digest(p) for p in b.PINNED},'initial':{str(p):b.digest(p) for p in (b.STOP,b.GUARD,b.COMPOSE,b.ACTIVE)},'controller_image':self.e['controller_image'],'disk_identity':{}}
        import struct
        for raw,ident in disks.items():
            p=Path(raw)
            if p.name=='seed.img':size=ident['size']
            else:
                with p.open('rb') as f:header=f.read(32)
                need(len(header)==32 and header[:4]==b'QFI\xfb','standalone qcow2 header required');size=struct.unpack('>Q',header[24:32])[0]
            fs=self.command(['/usr/bin/findmnt','-n','-o','UUID','--target',str(p)]).decode().strip();plan['disk_identity'][p.name]={'inode':ident['inode'],'virtual_bytes':size,'filesystem_uuid':fs}
        b.validate_plan(plan);self.transition_plan=plan;x.publish(self.job/'transition-plan.json',plan)
    def start_worker(self):
        need(self.command(['/usr/bin/systemctl','show','baarcha-cube-worker-01.service','-p','Job','--value']).strip() in (b'',b'0'),'worker job appeared')
        x.validate_start_authorization()
        self.command(['/usr/bin/systemctl','reset-failed','baarcha-cube-worker-01.service'])
        self.command(['/usr/bin/systemctl','start','baarcha-cube-worker-01.service'],30)
        host=b.Host(self.transition_plan)
        self.wait(lambda:host.generation(self.want_stop),240)
        need(not x.START_AUTH.exists(),'one-use start authorization was not consumed')
    def transition(self):
        d=ROOT/'boot-transitions'/self.job.name;d.mkdir(mode=0o700)
        host=b.Host(self.transition_plan)
        with host.worker_lock():result=b.transition(self.transition_plan,d,host)
        self.transition_result=result;return result
    def capture_after(self):
        # Heavy backup is deliberately separate for this incident recovery. The
        # optional before closure is retained, never described as a full backup.
        self.event('no-full-backup-claim',{'full_backup':False,'independent_restore_verified':False})
    def ready_fence(self):
        result=self.transition_result;need(result['tenant_ready'] is True,'new worker not ready')
        need(b.strict(b.http('/config/',2019))==self.routes['offline'],'routing reopened early')
        config=b.strict(b.trusted(b.STOP));need(config['controller_id']==result['controller_id'] and config['worker_boot_id']==result['worker_boot_id'],'new controller/boot pin changed')
        host=b.Host(self.transition_plan);observation=host.observe();need(observation.get('consistent') is True and observation.get('bindings')==len(self.plan['bindings']) and observation.get('worker_boot_id')==result['worker_boot_id'],'final bindings inconsistent')
        guard=b.strict(b.trusted(b.GUARD));host.fresh(guard,0)
        with b.locked(list(self.fds)):pass
    def reopen(self):
        self.ready_fence()
        proxy=self.inspect(self.plan['motion']['proxy_id']);need(proxy['Image']==self.plan['motion']['proxy_image'] and not proxy['State']['Paused'] and not proxy['State']['Restarting'],'Motion proxy generation changed')
        policy=self.baseline['motion_restart'];need(policy['Name'] in ('no','unless-stopped','always') and policy.get('MaximumRetryCount',0)==0,'unreviewed Motion restart policy')
        self.command(['/usr/bin/docker','update','--restart='+policy['Name'],proxy['Id']])
        if self.baseline['motion_running'] and not proxy['State']['Running']:self.command(['/usr/bin/docker','start',proxy['Id']])
        if not self.baseline['motion_running']:need(not proxy['State']['Running'],'originally stopped Motion proxy was unexpectedly started')
        for name,state in self.baseline['timers'].items():
            need(state in ('active','inactive'),'ambiguous original timer state')
            if state=='active':self.command(['/usr/bin/systemctl','start',name])
        self.reload('online')
        # Confirm restoration preserves the entire actual live route tree,
        # including exact Motion aliases absent from the startup Caddyfile.
        need(b.strict(b.http('/config/',2019))==self.routes['online'],'loaded online route restoration failed')
        self.ready_after_reopen()
    def ready_after_reopen(self):
        result=self.transition_result
        need(self.inspect('src-sandboxd-1')['Id']==result['controller_id'],'controller changed while reopening')
        need(b.http('/readyz',9090).strip()==b'ready','controller readiness unavailable after reopen')
        b.Host(self.transition_plan).fresh(b.strict(b.trusted(b.GUARD)),0)


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--plan',type=Path,required=True);parser.add_argument('--directory',type=Path,required=True);mode=parser.add_mutually_exclusive_group();mode.add_argument('--execute',action='store_true');mode.add_argument('--check',action='store_true');args=parser.parse_args()
    need(os.geteuid()==0,'native root required');plan=b.strict(b.trusted(args.plan));validate_plan(plan)
    if not args.execute and not args.check:
        print(json.dumps({'version':1,'plan_valid':True,'execution_authorized':False,'bindings':len(plan['bindings']),'full_backup':False}));return
    need(args.directory.parent==ROOT/'maintenance' and re.fullmatch(r'[a-z0-9-]{1,80}',args.directory.name),'fixed fresh maintenance generation required')
    need(args.directory.parent.resolve(strict=True)==args.directory.parent,'maintenance parent changed')
    with b.locked() as fds:
        args.directory.mkdir(mode=0o700)
        events=[];changed=[False]
        def event(phase,value=None):
            if phase=='drain-intent':changed[0]=True
            row={'version':1,'at':time.time(),'phase':phase,'value':value};x.publish(args.directory/('%03d-%s.json'%(len(events),phase)),row);events.append(row)
            b.atomic(args.directory/'current.json',b.encoded({'version':1,'phase':phase,'plan_sha256':b.sha(b.encoded(plan)),'locks_held':True,'full_backup':False}))
        host=Host(plan,args.directory,fds,event);sequence=Sequence(host,event)
        # Never let a generic TERM release maintenance locks after any mutation.
        def hold_signal(_number,_frame):
            if not changed[0]:raise KeyboardInterrupt
        signal.signal(signal.SIGTERM,hold_signal);signal.signal(signal.SIGINT,hold_signal)
        try:
            if args.check:
                check=host.preflight(defer_busy=True);event('read-only-preflight-'+('passed' if check['ready'] else 'deferred'),check);print(json.dumps({**check,'lifecycle_mutations':False,'locks_released_on_return':True}));return
            receipt=sequence.stop();result=sequence.start(receipt);print(json.dumps(result,sort_keys=True))
        except BaseException as error:
            if not changed[0]:raise
            refenced=False
            if any(row['phase']=='reopen-intent' for row in events):
                try:host.reload('offline');refenced=True
                except Exception:pass
            event('pending-operator-review',{'error_class':type(error).__name__,'error':str(error)[:2048],'offline_reload_confirmed':refenced,'source_retained':True})
            # Intentional lifetime fence. Recovery requires same-OFD handoff or
            # review of the actual pending phase; no blind rerun or rollback.
            while True:time.sleep(30)
        finally:
            if host.nested_context is not None and not changed[0]:host.nested_context.__exit__(None,None,None)

if __name__=='__main__':main()
