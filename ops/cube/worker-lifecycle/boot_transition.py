#!/usr/bin/env python3
"""Complete a proved clean worker reboot while routing remains fenced.

No drain, power, snapshot, guest mutation, image build/pull or route reload. The
caller is the root maintenance runner; it retains its locks and routing fence.
A private write-ahead journal permits exact continuation, never stale-pin rollback.
"""
import argparse
import contextlib
import copy
import datetime
import fcntl
import hashlib
import http.client as http_client
import importlib.util
import json
import os
from pathlib import Path
import re
import select
import stat
import struct
import subprocess
import time

ROOT = Path('/opt/baarcha-cube/worker-01')
SOURCE = Path('/opt/sandboxd/src')
STATE = Path('/opt/sandboxd/deploy-state')
STOP = Path('/etc/baarcha-cube/worker-stop.json')
START = Path('/etc/baarcha-cube/worker-start.json')
GUARD = Path('/etc/baarcha-cube/storage-guard.json')
COMPOSE = STATE / 'runtime-compose.json'
ACTIVE = STATE / 'active-images.json'
OFFLINE = ROOT / 'cutover-routing/offline.json'
OBSERVER = Path('/opt/baarcha-cube/storage-guard/observe.py')
LIFECYCLE = Path('/usr/local/libexec/baarcha-cube-worker-lifecycle.py')
COORDINATOR = Path('/usr/local/libexec/baarcha-cube-worker-start')
EXTERNAL = Path('/usr/local/libexec/baarcha-cube-external-clean.py')
SEQUENCE = Path('/var/lib/sandboxd/cube-storage-observer/sequence.json')
LOCKS = ('/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock',
         '/run/lock/cube-operator-acceptance.lock', '/opt/baarcha-bench/cube-workload-operator.lock')
TIMERS = tuple('baarcha-' + n + '.timer' for n in ('project-env-apply', 'classroom-egress', 'fennec-meet-egress'))
SERVICES = ('sandboxd', 'cube-management-api', 'cube-management-proxy')
PINNED = (START, SOURCE / '.env', SOURCE / 'docker-compose.yml',
          OFFLINE, OBSERVER, LIFECYCLE, COORDINATOR, EXTERNAL, Path('/usr/local/libexec/baarcha-cube-worker-stop'),
          Path('/etc/baarcha-cube/lifecycle.json'))
SHA = re.compile(r'[a-f0-9]{64}\Z')
UUID = re.compile(r'[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}\Z')
HEX = re.compile(r'[a-f0-9]{32}\Z')
SSH = ['/usr/bin/ssh', '-i', str(ROOT / 'operator-key'), '-p', '20222', '-oBatchMode=yes',
       '-oConnectTimeout=5', '-oServerAliveInterval=5', '-oServerAliveCountMax=2',
       '-oStrictHostKeyChecking=yes', '-oUserKnownHostsFile=' + str(ROOT / 'known_hosts'), 'root@127.0.0.1']

class Refused(RuntimeError):
    pass

def require(ok, message):
    if not ok:
        raise Refused(message)

def strict(raw):
    def pairs(entries):
        out = {}
        for key, value in entries:
            require(key not in out, 'duplicate JSON key')
            out[key] = value
        return out
    return json.loads(raw, object_pairs_hook=pairs)

def go_marker_hash(marker):
    # encoding/json marshals StopMarker in declaration order, unlike the map
    # originally written by stop. Preserve that concrete native evidence contract.
    keys=('version','phase','receipt_sha256','inventory_sha256','bindings','qemu_pid','qemu_start_time','worker_boot_id','worker_machine_id','data_uuid')
    binding_keys=('SandboxID','AppID','RuntimeID','TemplateID','Domain','ConfigSHA256','OwnerSHA256','ConfigRevision')
    ordered={k:marker[k] for k in keys}
    ordered['bindings']=[{k:b[k] for k in binding_keys} for b in marker['bindings']]
    raw=json.dumps(ordered,separators=(',',':'),ensure_ascii=False).replace('&','\\u0026').replace('<','\\u003c').replace('>','\\u003e').replace('\u2028','\\u2028').replace('\u2029','\\u2029')
    return sha(raw.encode())

def encoded(value):
    return (json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False) + '\n').encode()

def sha(raw):
    return hashlib.sha256(raw).hexdigest()

def trusted(path, private=True, limit=4*1024*1024):
    p = Path(path)
    require(p.is_absolute() and p.resolve(strict=True) == p, 'canonical operator path required')
    for ancestor in p.parents:
        s = ancestor.stat()
        require(s.st_uid == 0 and not s.st_mode & 0o022, 'operator ancestor is writable')
    fd = os.open(p, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as f:
        s = os.fstat(f.fileno())
        require(stat.S_ISREG(s.st_mode) and s.st_uid == 0 and s.st_nlink == 1 and not s.st_mode & 0o022,
                'untrusted operator file')
        require(not private or stat.S_IMODE(s.st_mode) == 0o600, 'private file must be root0600')
        require(s.st_size <= limit, 'oversized operator file')
        raw = f.read(limit+1)
        require(len(raw) <= limit, 'oversized operator file')
        return raw

def digest(path):
    # Executable artifacts can exceed the JSON bound; still refuse symlinks and
    # writable parents. Binary bytes never enter receipts or stdout.
    p = Path(path)
    require(p.resolve(strict=True) == p, 'noncanonical artifact')
    for ancestor in p.parents:
        s = ancestor.stat(); require(s.st_uid == 0 and not s.st_mode & 0o022, 'unsafe artifact ancestor')
    with p.open('rb') as f:
        s = os.fstat(f.fileno())
        require(stat.S_ISREG(s.st_mode) and s.st_uid == 0 and not s.st_mode & 0o022, 'unsafe artifact')
        h = hashlib.sha256()
        for b in iter(lambda: f.read(1024*1024), b''): h.update(b)
        return h.hexdigest()

def atomic(path, raw):
    p = Path(path)
    require(p.parent.resolve(strict=True) == p.parent, 'noncanonical output parent')
    s = p.parent.stat(); require(s.st_uid == 0 and not s.st_mode & 0o022, 'unsafe output parent')
    if p.exists() or p.is_symlink(): trusted(p)
    tmp = p.with_name('.'+p.name+'.boot-'+str(os.getpid()))
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(raw); f.flush(); os.fsync(f.fileno())
        os.replace(tmp, p)
        d = os.open(p.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try: os.fsync(d)
        finally: os.close(d)
    finally:
        if tmp.exists(): tmp.unlink()

def validate_plan(p):
    require(set(p) == {'version','outer_machine_id','files','initial','controller_image','disk_identity'}, 'unexpected transition plan fields')
    require(p['version'] == 1 and HEX.fullmatch(p['outer_machine_id']) and p['outer_machine_id'] != '0'*32, 'invalid host identity')
    require(set(p['files']) == set(map(str,PINNED)) and all(SHA.fullmatch(v) for v in p['files'].values()), 'exact executable/config artifact pins required')
    require(set(p['initial']) == {str(STOP),str(GUARD),str(COMPOSE),str(ACTIVE)} and all(SHA.fullmatch(v) for v in p['initial'].values()), 'exact old mutable config pins required')
    require(re.fullmatch(r'sha256:[a-f0-9]{64}', p['controller_image']), 'immutable controller image required')
    require(set(p['disk_identity']) == {'root.qcow2','data.qcow2','seed.img'}, 'all fixed worker disks required')
    for value in p['disk_identity'].values():
        require(set(value)=={'inode','virtual_bytes','filesystem_uuid'} and type(value['inode']) is int and value['inode'] > 0 and type(value['virtual_bytes']) is int and value['virtual_bytes'] > 0 and UUID.fullmatch(value['filesystem_uuid']), 'invalid disk identity')

def validate_chain(stop, start, marker, pause, clean, drain, generation):
    """Validate existing lifecycle receipts; an observation alone is never trust."""
    require(start['version']==1 and marker['version']==1 and marker['phase']=='preparing' and pause['version']==1 and pause['verified'] is True and pause['provider_jobs']==0, 'complete clean-stop proof required')
    require(clean['version']==1 and clean['state'] in ('stopped-clean','externally-stopped-clean') and clean['proof']==pause, 'exact clean exit receipt required')
    expected_start={'version','pause_proof','clean_receipt'}
    if clean['state']=='externally-stopped-clean':
        expected_start.add('external_verifier_sha256')
        require(SHA.fullmatch(start.get('external_verifier_sha256','')) and digest(EXTERNAL)==start['external_verifier_sha256'],'external verifier differs from explicit start configuration')
        require(load_module(EXTERNAL).validate_receipt(start['clean_receipt'])['receipt']==clean,'complete external clean evidence required')
    else: require('external' not in clean,'supervisor receipt cannot impersonate external evidence')
    require(set(start)==expected_start,'unexpected startup configuration fields')
    require(drain['version']==1 and all(drain.get(k) is True for k in ('traffic_fenced','existing_requests_drained','direct_writers_fenced','provider_jobs_drained')), 'complete pre-stop drain required')
    for key in ('worker_machine_id','data_uuid','worker_boot_id','qemu_pid','qemu_start_time'):
        require(marker[key]==pause[key]==stop[key], 'stop marker/proof generation differs')
    for key in ('controller_id','worker_boot_id','qemu_pid','qemu_start_time'):
        require(drain[key]==stop[key], 'drain bound to another generation')
    require(marker['inventory_sha256']==pause['inventory_sha256']==drain['inventory_sha256'] and SHA.fullmatch(marker['inventory_sha256']), 'inventory proof differs')
    require(marker['receipt_sha256']==pause['receipt_sha256'] and SHA.fullmatch(marker['receipt_sha256']), 'drain proof hash differs')
    require(type(marker['bindings']) is list, 'binding inventory missing')
    ids = [b['RuntimeID'] for b in marker['bindings']]
    require(len(ids)==len(set(ids)) and pause['guest_states']==dict.fromkeys(ids,'paused'), 'every exact retained guest must be paused')
    generated = datetime.datetime.fromisoformat(pause['generated_at'].replace('Z','+00:00')).timestamp()
    require(0 < generated <= clean['generated_at'] <= time.time()+1, 'invalid clean-stop chronology')
    require(generation['worker_machine_id']==stop['worker_machine_id'] and generation['inner_fs_uuid']==stop['data_uuid'] and generation['worker_boot_id']!=stop['worker_boot_id'], 'same worker/storage and genuinely new boot required')
    require((generation['qemu_pid'],generation['qemu_start_time'])!=(stop['qemu_pid'],stop['qemu_start_time']), 'QEMU generation unchanged')

def new_configs(stop, guard, compose, generation, active=None):
    require(stop['admission']['storage_guard']==guard, 'stop and observer policy differ')
    env = compose['services']['sandboxd']['environment']
    require(type(env) is dict and strict(env['SANDBOXD_CUBE_ADMISSION'])==stop['admission'], 'controller and native admission policy differ')
    require(guard['expected_boot_id']==stop['worker_boot_id'] and guard['worker_machine_id']==stop['worker_machine_id'] and guard['inner_fs_uuid']==stop['data_uuid'], 'old worker pins inconsistent')
    outstop, outguard, outcompose = copy.deepcopy(stop), copy.deepcopy(guard), copy.deepcopy(compose)
    outguard['expected_boot_id'] = generation['worker_boot_id']
    outguard['outer_boot_id'] = generation['outer_boot_id']
    outstop.update(worker_boot_id=generation['worker_boot_id'], qemu_pid=generation['qemu_pid'], qemu_start_time=generation['qemu_start_time'])
    outstop['admission']['storage_guard'] = copy.deepcopy(outguard)
    outcompose['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'] = encoded(outstop['admission']).decode().strip()
    wanted={str(STOP):outstop,str(GUARD):outguard,str(COMPOSE):outcompose}
    if active is not None:
        outactive=copy.deepcopy(active)
        environment=outactive['services']['sandboxd'].get('environment',{})
        if 'SANDBOXD_CUBE_ADMISSION' in environment:
            require(strict(environment['SANDBOXD_CUBE_ADMISSION'])==stop['admission'],'active image overlay admission differs from reviewed policy')
            environment['SANDBOXD_CUBE_ADMISSION']=encoded(outstop['admission']).decode().strip()
        wanted[str(ACTIVE)]=outactive
    return wanted

class Journal:
    """Persist desired bytes BEFORE each mutation; exact old/new CAS only.

    A mixed three-file state fails admission closed. Resume rolls forward from
    the immutable original bytes, never re-bases onto unrelated operator edits.
    """
    def __init__(self, directory, plan, read=None, write=None):
        self.directory=Path(directory); self.path=self.directory/'transition.json'; self.read=read or trusted; self.write=write or atomic; self.plan=plan
        self.value = strict(self.read(self.path)) if self.path.exists() else None
        if self.value is not None:
            require(self.value['plan_sha256']==sha(encoded(plan)), 'pending journal belongs to another plan')
    def save(self): self.write(self.path, encoded(self.value))
    def begin(self, originals, wanted, generation, proof_hashes, details):
        require(self.value is None, 'transition already initialized')
        self.value={'version':1,'phase':'pending','plan_sha256':sha(encoded(self.plan)), 'generation':generation,
                    'originals':{k:v.decode() for k,v in originals.items()}, 'wanted':wanted, 'proof_hashes':proof_hashes, 'reconciled':None, 'controller_attempted':False, 'controller_id':None, 'tenant_ready':False, **details}
        self.save()
    def apply(self):
        for path, value in self.value['wanted'].items():
            old=self.value['originals'][path].encode(); new=encoded(value); current=self.read(path)
            require(current in (old,new), 'configuration drift; pending journal retained')
            if current != new: self.write(path,new)
        self.value['phase']='pins-advanced'; self.save()
    def controller_pin(self, ident):
        require(SHA.fullmatch(ident), 'invalid new controller identity')
        # Record the new target before changing stop.json so interruption resumes.
        self.value['controller_id']=ident
        self.value['wanted'][str(STOP)]['controller_id']=ident
        self.value['phase']='controller-pinned'; self.save()
        old=encoded({**self.value['wanted'][str(STOP)],'controller_id':strict(self.value['originals'][str(STOP)])['controller_id']})
        now=self.read(STOP); new=encoded(self.value['wanted'][str(STOP)])
        require(now in (old,new), 'stop config changed before controller CAS')
        if now != new: self.write(STOP,new)

@contextlib.contextmanager
def locked(inherited=None):
    owned=[]
    try:
        require(inherited is None or len(inherited)==len(LOCKS), 'all four inherited lock descriptors required')
        for i,path in enumerate(LOCKS):
            fd=os.open(path,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600) if inherited is None else inherited[i]
            if inherited is None: owned.append(fd)
            a,b=os.fstat(fd),os.stat(path,follow_symlinks=False)
            require(stat.S_ISREG(a.st_mode) and a.st_uid==0 and not a.st_mode&0o022 and (a.st_dev,a.st_ino)==(b.st_dev,b.st_ino),'lock descriptor/path differs')
            if inherited is not None:
                # First prove some exclusive holder existed BEFORE this helper:
                # an independent shared contender must be refused. Then the
                # inherited OFD's EX check proves it is that holder, not another
                # process's exclusive lock. Never upgrade an unlocked/shared FD
                # and call it continuous maintenance ownership.
                probe=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
                try:
                    p=os.fstat(probe)
                    require((p.st_dev,p.st_ino)==(a.st_dev,a.st_ino),'lock changed during inherited check')
                    try: fcntl.flock(probe,fcntl.LOCK_SH|fcntl.LOCK_NB)
                    except BlockingIOError: pass
                    else: raise Refused('inherited descriptor was not already exclusively held')
                finally: os.close(probe)
            fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
            # Calling flock on the SAME inherited OFD retains the parent's lock;
            # closing only locally-owned FDs avoids unlocking the parent later.
        yield list(inherited) if inherited is not None else list(owned)
    finally:
        for fd in reversed(owned): os.close(fd)

def run(argv,timeout=30):
    p=subprocess.run(argv,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=timeout,check=True)
    require(len(p.stdout)<=4*1024*1024,'oversized command output')
    return p.stdout

def http(path,port):
    c=http_client.HTTPConnection('127.0.0.1',port,timeout=5)
    try:
        c.request('GET',path); r=c.getresponse(); data=r.read(4*1024*1024+1)
        require(r.status==200 and len(data)<=4*1024*1024,'local read-only readiness unavailable')
        return data
    finally: c.close()

def load_module(path):
    path=Path(path)
    spec=importlib.util.spec_from_file_location('boot_'+path.stem,path); module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module); return module

class Host:
    def __init__(self,plan): self.plan=plan; self.nested=None
    def command(self,args,timeout=30): return run(args,timeout)
    def compose(self,*args):
        return self.command(['/usr/bin/docker','compose','-p','src','--env-file',str(SOURCE/'.env'),'-f',str(SOURCE/'docker-compose.yml'),'-f',str(COMPOSE),'-f',str(STATE/'active-images.json'),*args],120)
    def inspect(self,name): return strict(self.command(['/usr/bin/docker','inspect',name]))[0]
    def check_files(self):
        for path,expected in self.plan['files'].items(): require(digest(path)==expected,'pinned helper/source/config changed')
        require(Path('/etc/machine-id').read_text().strip()==self.plan['outer_machine_id'],'outer host changed')
        for name,ident in self.plan['disk_identity'].items():
            p=(Path('/mnt/nvme/baarcha-cube/worker-01') if name=='data.qcow2' else ROOT)/name
            require(p.resolve(strict=True)==p,'worker disk path changed')
            s=p.stat(); require(stat.S_ISREG(s.st_mode) and s.st_uid==0 and s.st_ino==ident['inode'],'worker disk backing identity changed')
            if name=='seed.img': virtual=s.st_size
            else:
                with p.open('rb') as file: header=file.read(32)
                require(len(header)==32 and header[:4]==b'QFI\xfb','fixed disk is not qcow2')
                virtual=struct.unpack('>Q',header[24:32])[0]
            require(virtual==ident['virtual_bytes'],'worker virtual disk size changed')
            actual=self.command(['/usr/bin/findmnt','-n','-o','UUID','--target',str(p)]).decode().strip()
            require(actual==ident['filesystem_uuid'],'worker disk filesystem changed')
    def fence(self):
        self.check_files()
        require(strict(http('/config/',2019))==strict(trusted(OFFLINE,False)),'reviewed offline routing not loaded')
        for timer in TIMERS:
            for unit in (timer,timer.replace('.timer','.service')):
                require(self.command(['/usr/bin/systemctl','show',unit,'-p','ActiveState','--value']).strip()==b'inactive','scheduled writer remains active')
        active=self.command(['/usr/bin/systemctl','list-units','--plain','--no-legend','--state=activating,active','cube-coding-profile-*']).strip()
        require(not active,'coding profile still active')
        if self.nested is not None: require(self.nested.poll() is None,'nested operator lock lost')
    def old_controller(self,stop):
        cp=self.inspect('src-sandboxd-1')
        require(cp['Id']==stop['controller_id'] and cp['Image']==self.plan['controller_image'],'controller identity/image changed')
        require(not cp['State']['Running'] and not cp['State'].get('Paused') and not cp['State'].get('Restarting') and cp['State']['Status'] in ('exited','created') and cp['HostConfig']['RestartPolicy']['Name']=='no','exact stopped controller with restart disabled required')
        return cp
    def generation(self,stop):
        life=load_module(LIFECYCLE); status=strict(trusted(ROOT/'lifecycle-status.json'))
        require(status.get('state')=='running-unreconciled' and status.get('tenant_ready') is False,'worker supervisor does not report an unreconciled boot')
        pid=status['qemu_pid']; ticks=life.process_start_time(pid)
        supervisor=self.command(['/usr/bin/systemctl','show','baarcha-cube-worker-01.service','-p','MainPID','--value']).decode().strip()
        require(supervisor==str(status['supervisor_pid']) and supervisor not in ('','0'),'worker supervisor identity changed')
        process=Path('/proc/'+str(pid)+'/status').read_text().splitlines()
        require([v.split()[1] for v in process if v.startswith('PPid:')]==[supervisor],'QEMU is not the supervised child')
        require(type(pid)is int and pid>1 and str(ticks).isdigit(),'invalid QEMU generation')
        require(life.process_start_time(pid)==str(ticks),'QEMU PID reused')
        actual=Path('/proc/'+str(pid)+'/cmdline').read_bytes().split(b'\0')[:-1]
        require(actual==[v.encode() for v in life.fixed_qemu()],'QEMU launch differs from reviewed disks/resource policy')
        require(Path('/proc/'+str(pid)+'/exe').resolve(strict=True)==Path('/usr/bin/qemu-system-x86_64').resolve(strict=True) and digest('/usr/bin/qemu-system-x86_64')==strict(trusted('/etc/baarcha-cube/lifecycle.json'))['qemu_sha256'],'QEMU executable differs')
        observer=load_module(OBSERVER); inner=observer.probe_inner(); outer=observer.probe_outer()
        guard=strict(trusted(GUARD))
        require(inner['worker_machine_id']==stop['worker_machine_id'] and inner['inner_fs_uuid']==stop['data_uuid'] and outer['outer_fs_uuid']==guard['outer_fs_uuid'],'worker/storage identity differs')
        require(UUID.fullmatch(inner['worker_boot_id']),'invalid actual worker boot')
        return {'worker_machine_id':inner['worker_machine_id'],'inner_fs_uuid':inner['inner_fs_uuid'],'worker_boot_id':inner['worker_boot_id'],'outer_boot_id':Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'qemu_pid':pid,'qemu_start_time':str(ticks)}
    @contextlib.contextmanager
    def worker_lock(self):
        # The foreground SSH process holds ONLY the fixed nested operator lock.
        # Its stdin lifetime is bound to this parent; no detached lock process.
        code="import fcntl,os,sys,stat; p='/run/lock/cube-operator-acceptance.lock'; f=os.open(p,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600); s=os.fstat(f); assert stat.S_ISREG(s.st_mode) and s.st_uid==0 and not s.st_mode&18; fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB); print('locked',flush=True); sys.stdin.buffer.read(); os.close(f)"
        import shlex
        p=subprocess.Popen(SSH+['/usr/bin/python3 -c '+shlex.quote(code)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
        self.nested=p
        try:
            ready,_,_=select.select([p.stdout],[],[],15)
            require(ready and p.stdout.readline()==b'locked\n' and p.poll() is None,'nested operator lock unavailable')
            yield
        finally:
            if p.stdin: p.stdin.close()
            try:p.wait(timeout=15)
            except subprocess.TimeoutExpired: p.terminate(); p.wait(timeout=5)
            self.nested=None
    def fresh(self,guard,minimum):
        o=strict(trusted(guard['observation_path'])); now=time.clock_gettime_ns(time.CLOCK_BOOTTIME)
        require(o['version']==1 and o['generation']>minimum,'fresh non-reset observer generation required')
        pins={'observer_id':'observer_id','worker_machine_id':'worker_machine_id','worker_boot_id':'expected_boot_id','outer_boot_id':'outer_boot_id','inner_fs_uuid':'inner_fs_uuid','outer_fs_uuid':'outer_fs_uuid'}
        require(all(o[k]==guard[v] for k,v in pins.items()) and o['outer_boot_id']==Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'observation pins differ')
        require(0<o['started_boottime_ns']<=o['completed_boottime_ns']<=now and now-o['started_boottime_ns']<=30_000_000_000 and min(o['inner_free_bytes'],o['outer_free_bytes'])>=96*1024**3,'observation stale or reserve too low')
        return o
    def refresh_observer(self): self.command(['/usr/bin/systemctl','start','baarcha-cube-storage-observer.service'],30)
    def reconcile(self): return strict(self.command([str(COORDINATOR)],200))
    def recreated(self,expected_env):
        cp=self.inspect('src-sandboxd-1')
        require(cp['Image']==self.plan['controller_image'] and cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name']=='unless-stopped','new controller not running exact image')
        actual=dict(v.split('=',1) for v in cp['Config']['Env'])
        require(actual==expected_env,'controller environment changed beyond reviewed boot pins')
        require(cp['Config'].get('User','') in ('','0','root','0:0','root:root') and cp['HostConfig'].get('UsernsMode')=='host','controller observer identity differs')
        mount=[m for m in cp['Mounts'] if m['Destination']=='/run/sandboxd-cube-storage']
        require(len(mount)==1 and mount[0]['Source']=='/run/sandboxd-cube-storage' and mount[0]['RW'] is False,'observer mount missing or writable')
        for name in SERVICES[1:]:
            ident=self.compose('ps','-q',name).decode().strip(); relay=self.inspect(ident)
            spec=strict(self.compose('config','--format','json'))['services'][name]
            expected_image=self.command(['/usr/bin/docker','image','inspect','--format','{{.Id}}',spec['image']]).decode().strip()
            require(relay['Image']==expected_image and relay['State']['Running'] and relay['State'].get('Health',{}).get('Status')=='healthy','relay not healthy/exact image')
            h=relay['HostConfig']; require(h['NetworkMode']=='container:'+cp['Id'] and not h.get('PortBindings') and h['ReadonlyRootfs'] and h['CapDrop']==['ALL'] and 'no-new-privileges:true' in h.get('SecurityOpt',[]),'relay namespace/security differs')
            require(relay['Config']['User']=='65532:65532' and h.get('UsernsMode')=='host' and h.get('GroupAdd')==['982'],'relay UID/GID differs')
            mounts=[m for m in relay['Mounts'] if m['Destination']=='/run/cube-management']
            require(len(mounts)==1 and mounts[0]['Source']=='/run/cube-management' and mounts[0]['RW'] is False,'relay socket mount differs')
            require(os.readlink('/proc/'+str(relay['State']['Pid'])+'/ns/net')==os.readlink('/proc/'+str(cp['State']['Pid'])+'/ns/net'),'actual relay net namespace differs')
        require(http('/healthz',9090).strip()==b'ok' and http('/readyz',9090).strip()==b'ready','controller health not ready')
        return cp['Id']
    def activate(self):
        self.compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','sandboxd')
        self.compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate',*SERVICES[1:])
    def observe(self): return strict(self.command([str(COORDINATOR),'--observe'],200))


def transition(plan,directory,host):
    validate_plan(plan); host.fence()
    j=Journal(directory,plan)
    if j.value is None:
        originals={p:trusted(p) for p in plan['initial']}
        require(all(sha(raw)==plan['initial'][p] for p,raw in originals.items()),'initial config changed since review')
        stop,guard,compose=(strict(originals[str(p)]) for p in (STOP,GUARD,COMPOSE))
        cp=host.old_controller(stop); generation=host.generation(stop)
        start=strict(trusted(START)); paths=[stop['database']+'.worker-stop.json',start['pause_proof'],start['clean_receipt'],stop['receipt']]
        raw=[trusted(p) for p in paths]; marker,pause,clean,drain=map(strict,raw)
        validate_chain(stop,start,marker,pause,clean,drain,generation)
        # Native coordinator subsequently checks Go's exact receipt/inventory
        # hashes and the canonical current SQLite/provider binding set.
        require(sha(trusted(OFFLINE,False))==plan['files'][str(OFFLINE)],'offline config changed')
        sequence=strict(trusted(SEQUENCE))
        require(sequence['observer_id']==guard['observer_id'] and type(sequence['generation'])is int and sequence['generation']>=1,'enrolled observer sequence missing/corrupt')
        wanted=new_configs(stop,guard,compose,generation,strict(originals[str(ACTIVE)]))
        j.begin(originals,wanted,generation,{p:sha(v) for p,v in zip(paths,raw)},dict(old_sequence=sequence['generation'],old_environment=dict(v.split('=',1) for v in cp['Config']['Env']),marker_path=paths[0],inventory_sha256=marker['inventory_sha256'],marker=marker,reconcile_attempted=False))
    v=j.value
    require(v['phase']!='complete','transition already complete; do not replay')
    oldstop=strict(v['originals'][str(STOP)])
    require(host.generation(oldstop)==v['generation'],'pending transition is for another worker generation')
    for path,expected in v['proof_hashes'].items():
        if path==v['marker_path'] and not Path(path).exists() and v['reconcile_attempted']: continue
        require(sha(trusted(path))==expected,'immutable lifecycle proof changed')
    host.fence()
    # Partial CAS resumes only before any recreation, when the exact old
    # controller remains stopped. After recreation no config rebasing is allowed.
    if not v['controller_attempted']:
        host.old_controller(oldstop); j.apply()
    else:
        if v['controller_id'] is not None: j.controller_pin(v['controller_id'])
        for path,value in v['wanted'].items(): require(trusted(path)==encoded(value),'post-recreation config drift')
    host.refresh_observer(); host.fresh(v['wanted'][str(GUARD)],v['old_sequence'])
    if v['reconciled'] is None:
        host.fence()
        if v['reconcile_attempted'] and not Path(v['marker_path']).exists():
            # Native coordinator persists this evidence before exact marker
            # removal. Its old-marker hash proves this interrupted invocation.
            proof=strict(trusted(Path(oldstop['evidence_directory'])/'startup-current.json'))
            require(proof.get('stop_marker_sha256')==go_marker_hash(v['marker']),'startup receipt differs from exact stop marker')
        else:
            v['reconcile_attempted']=True; j.save(); proof=host.reconcile()
        require(proof.get('tenant_ready') is True and proof.get('guests_woken')==0 and proof.get('routing_changed') is False and proof.get('inventory_sha256')==v['inventory_sha256'] and proof.get('worker_boot_id')==v['generation']['worker_boot_id'] and proof.get('qemu_pid')==v['generation']['qemu_pid'] and proof.get('qemu_start_time')==v['generation']['qemu_start_time'],'native startup reconciliation incomplete')
        v['reconciled']=proof; v['phase']='reconciled'; j.save()
    expected=copy.deepcopy(v['old_environment'])
    expected['SANDBOXD_CUBE_ADMISSION']=v['wanted'][str(COMPOSE)]['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION']
    if not v['controller_attempted']:
        host.fence(); v['controller_attempted']=True; v['phase']='recreating'; j.save(); host.activate()
    # A failed/ambiguous compose call is NOT automatically replayed. On resume,
    # only exact healthy resulting identities permit continuation; otherwise the
    # operator repairs under the retained fence/locks using the saved journal.
    deadline=time.monotonic()+60
    while True:
        try: ident=host.recreated(expected); break
        except (Refused,subprocess.SubprocessError):
            if time.monotonic()>=deadline: raise
            time.sleep(1)
    j.controller_pin(ident)
    host.fence(); host.fresh(v['wanted'][str(GUARD)],v['old_sequence'])
    observation=host.observe()
    require(observation.get('consistent') is True and observation.get('worker_boot_id')==v['generation']['worker_boot_id'] and type(observation.get('active')) is int and 0<=observation['active']<=4 and observation.get('bindings')==len(v['marker']['bindings']),'binding/admission verification failed after controller restart')
    host.fence(); host.fresh(v['wanted'][str(GUARD)],v['old_sequence'])
    v['phase']='complete'; v['tenant_ready']=True; v['routing_changed']=False; v['active_after_controller_start']=observation['active']; j.save()
    return {'version':1,'tenant_ready':True,'routing_changed':False,'guests_woken_during_offline_reconciliation':0,'active_after_controller_start':observation['active'],'controller_id':ident,'worker_boot_id':v['generation']['worker_boot_id'],'journal_sha256':sha(encoded(v))}


def main():
    p=argparse.ArgumentParser(description=__doc__); p.add_argument('--plan',type=Path,required=True);p.add_argument('--directory',type=Path,required=True);p.add_argument('--inherited-lock-fds');p.add_argument('--execute',action='store_true');args=p.parse_args()
    require(os.geteuid()==0 and args.execute,'root and explicit execute required')
    require(args.directory.is_relative_to(ROOT/'boot-transitions') and args.directory.parent==ROOT/'boot-transitions' and re.fullmatch(r'[a-z0-9-]{1,80}',args.directory.name),'fixed private transition directory required')
    plan=strict(trusted(args.plan)); validate_plan(plan)
    # Caller prepares an empty root0700 directory; never adopt unexpected data.
    s=args.directory.stat();require(args.directory.resolve(strict=True)==args.directory and s.st_uid==0 and stat.S_IMODE(s.st_mode)==0o700,'private existing transition directory required')
    inherited=[int(x) for x in args.inherited_lock_fds.split(',')] if args.inherited_lock_fds else None
    with locked(inherited):
        host=Host(plan)
        with host.worker_lock():
            result=transition(plan,args.directory,host)
            print(json.dumps(result,sort_keys=True))

if __name__=='__main__':
    try: main()
    except (Refused,OSError,ValueError,KeyError,TypeError,subprocess.SubprocessError):
        raise SystemExit('Clean boot transition refused; retain routing fence and pending journal. No guest recovery or stale-pin rollback performed.')
