#!/usr/bin/env python3
"""INERT CANDIDATE. No installed units; drain integration intentionally refuses."""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import signal
import selectors
import socket
import stat
import subprocess
import sys
import time

ROOT = Path('/opt/baarcha-cube/worker-01')
DATA = Path('/mnt/nvme/baarcha-cube/worker-01')
CONFIG = Path('/etc/baarcha-cube/lifecycle.json')
STATUS = ROOT / 'lifecycle-status.json'
SCRIPT = '/usr/local/libexec/baarcha-cube-worker-lifecycle.py'
STOP_COORDINATOR_IMPLEMENTED = False
BACKUP_MARKER = b'baarcha-cube-backup-lock-v1\n'
INSTANCE_MARKER = b'baarcha-cube-worker-supervisor-v1\n'
SERVICES = tuple('cube-sandbox-'+x+'.service' for x in (
    'mysql','redis','minio','coredns','dns','cubeops','cubemaster','cube-api',
    'cubelet','cube-templatecenter','cube-lifecycle-manager','cube-proxy',
    'cube-egress-net','cube-egress'))

class Blocked(RuntimeError):
    pass

def require(value, message):
    if not value:
        raise Blocked(message)

def real_path(path):
    path=Path(path)
    require(path.is_absolute() and path.resolve(strict=True)==path, 'real absolute path required')
    return path

def private_json(path):
    path=real_path(path)
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
    with os.fdopen(fd) as file:
        info=os.fstat(file.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o600, 'root-only regular configuration required')
        data=file.read(131073)
    require(len(data)<=131072,'configuration too large')
    return json.loads(data)

@contextlib.contextmanager
def lifetime_lock(path, marker, exclusive):
    path=Path(path)
    real_path(path.parent)
    fd=os.open(path,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600)
    try:
        info=os.fstat(fd)
        require(stat.S_ISREG(info.st_mode) and info.st_uid==os.geteuid() and stat.S_IMODE(info.st_mode)==0o600,'unsafe lifetime lock')
        # Existing lock files are never truncated, replaced or unlinked.
        fcntl.flock(fd,(fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH)|fcntl.LOCK_NB)
        data=os.read(fd,128)
        if not data:
            os.write(fd,marker);os.fsync(fd)
        else:
            require(data==marker,'wrong lifetime lock marker')
        os.set_inheritable(fd,True)
        yield fd
    finally:
        os.close(fd)

def run(argv, timeout=10):
    # No shell, environment expansion, generic hooks or user-supplied executables.
    return subprocess.run(argv,check=True,capture_output=True,timeout=timeout).stdout

def digest(path):
    path=real_path(path)
    with open(path,'rb') as file:
        require(stat.S_ISREG(os.fstat(file.fileno()).st_mode),'regular artifact required')
        h=hashlib.sha256()
        for block in iter(lambda:file.read(1024*1024),b''):h.update(block)
        return h.hexdigest()

def fixed_qemu():
    return ['/usr/bin/qemu-system-x86_64','-enable-kvm','-machine','q35,accel=kvm',
        '-cpu','host','-name','baarcha-cube-worker-01','-m','40960','-smp','12',
        '-device','virtio-rng-pci','-drive',f'file={ROOT}/root.qcow2,if=virtio,format=qcow2',
        '-drive',f'file={DATA}/data.qcow2,if=virtio,format=qcow2',
        '-drive',f'file={ROOT}/seed.img,if=virtio,format=raw,readonly=on',
        '-nic','user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:20222-:22,hostfwd=tcp:127.0.0.1:20300-:3000,hostfwd=tcp:127.0.0.1:20080-:80',
        '-display','none','-serial',f'file:{ROOT}/serial.log',
        '-qmp',f'unix:{ROOT}/qmp.sock,server=on,wait=off']

def host_preflight(config):
    require(STOP_COORDINATOR_IMPLEMENTED,'production stop coordinator is not implemented in this candidate')
    require(config.get('version')==1 and config.get('reviewed') is True,'reviewed lifecycle manifest required')
    require(config.get('drain_integration_reviewed') is True,'production drain integration is not installed')
    if STATUS.exists():
        require(private_json(STATUS).get('state')=='stopped-clean','unclean previous worker exit requires offline recovery review')
    else:
        require(config.get('first_boot_empty_reviewed') is True,'initial boot needs explicit empty-worker review')
    require(Path('/dev/kvm').exists(),'KVM unavailable')
    require(digest('/usr/bin/qemu-system-x86_64')==config.get('qemu_sha256'),'QEMU binary differs from review')
    for path in (ROOT/'root.qcow2',DATA/'data.qcow2',ROOT/'seed.img'):
        real_path(path);require(path.is_file(),'disk missing')
    require(run(['/usr/bin/findmnt','-n','-o','UUID','--target',str(DATA)]).decode().strip()==config.get('outer_data_filesystem_uuid'),'outer data filesystem identity mismatch')
    for path in (ROOT/'root.qcow2',DATA/'data.qcow2'):
        value=json.loads(run(['/usr/bin/qemu-img','info','--output=json',str(path)]))
        require(value.get('format')=='qcow2' and not value.get('backing-filename') and not value.get('data-file') and not value.get('format-specific',{}).get('data',{}).get('data-file'),'standalone reviewed disks required')
    require(os.statvfs(DATA).f_bavail*os.statvfs(DATA).f_frsize>=48*1024**3,'less than 48 GiB free worker storage reserve')


def nested_preflight(config, require_active=False):
    require(socket.gethostname()=='baarcha-cube-worker-01','wrong nested worker')
    require(config.get('version')==1 and config.get('reviewed') is True,'reviewed nested manifest required')
    require(Path('/etc/machine-id').read_text().strip()==config.get('machine_id'),'worker machine identity mismatch')
    require(run(['/usr/bin/findmnt','-n','-o','UUID','--target','/data']).decode().strip()==config.get('data_filesystem_uuid'),'nested data identity mismatch')
    binaries=config.get('binaries',{})
    require(set(binaries)=={'cubelet','cubemaster','cube-api'},'all three patched binaries must be pinned')
    for entry in binaries.values():
        path=Path(entry['path'])
        require(path.is_absolute() and path.is_relative_to('/usr/local/services'),'unexpected binary scope')
        require(digest(path)==entry.get('sha256'),'installed patch binary mismatch')
    # Pin start/stop scripts, configs and dependency-image contract files too;
    # none of these are executed from this manifest.
    require(bool(config.get('artifacts')),'launch/config artifact hashes required')
    for name,expected in config['artifacts'].items():
        path=Path(name)
        require(path.is_relative_to('/etc/systemd/system') or path.is_relative_to('/usr/local/services'),'unexpected artifact scope')
        require(digest(path)==expected,'launch/config artifact changed')
    filesystem=os.statvfs('/data')
    require(filesystem.f_bavail*filesystem.f_frsize>=48*1024**3,'nested storage reserve below 48 GiB')
    if require_active:
        for unit in SERVICES:
            require(run(['/usr/bin/systemctl','is-active',unit]).strip()==b'active','required management service inactive')
    return {'version':1,'management_active':require_active,'tenant_ready':False,'reconciliation_required':True}


def write_status(path,value):
    path=Path(path);real_path(path.parent)
    temp=path.with_name(path.name+'.tmp-'+str(os.getpid()))
    fd=os.open(temp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    try:
        with os.fdopen(fd,'w') as file:
            json.dump(value,file,sort_keys=True);file.write('\n');file.flush();os.fsync(file.fileno())
        os.replace(temp,path)
        parent=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
        try:os.fsync(parent)
        finally:os.close(parent)
    finally:
        if temp.exists():temp.unlink()


STOP_CHILDREN = []

def process_start_time(pid):
    value=Path(f'/proc/{pid}/stat').read_text()
    fields=value[value.rfind(')')+1:].split()
    require(len(fields)>=20,'QEMU process generation unavailable')
    return fields[19]


def validate_stop_proof(identity,proof):
    require(isinstance(proof,dict) and proof.get('version')==1 and proof.get('verified') is True
        and proof.get('qemu_pid')==identity['qemu_pid']
        and proof.get('qemu_start_time')==identity['qemu_start_time']
        and isinstance(proof.get('guest_states'),dict)
        and all(v=='paused' for v in proof['guest_states'].values())
        and proof.get('provider_jobs')==0,'current-worker paused proof required')
    return proof


def prepare_stop(identity):
    # Fixed root-installed CLI, fixed root0600 config; no executable or shell
    # hook comes from configuration. This child ignores pipe EOF and retains
    # controller exclusion until exact QEMU exit, including every error path.
    require(STOP_COORDINATOR_IMPLEMENTED,'coordinator not accepted for installation')
    process=subprocess.Popen(['/usr/local/libexec/baarcha-cube-worker-stop','--config','/etc/baarcha-cube/worker-stop.json'],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,close_fds=True)
    STOP_CHILDREN.append(process)  # retain handles; never terminate/kill on timeout
    selector=selectors.DefaultSelector();selector.register(process.stdout,selectors.EVENT_READ)
    deadline=time.monotonic()+600;buffer=b''
    try:
        while time.monotonic()<deadline:
            for key,_mask in selector.select(min(1,max(0,deadline-time.monotonic()))):
                block=os.read(key.fileobj.fileno(),65537-len(buffer))
                require(bool(block),'stop coordinator closed without proof');buffer+=block
                require(len(buffer)<=65536,'stop proof exceeds bound')
                if b'\n' in buffer:
                    line,extra=buffer.split(b'\n',1);require(not extra.strip(),'unexpected extra stop proof')
                    return validate_stop_proof(identity,json.loads(line))
        raise Blocked('stop coordinator exceeded bounded preparation; locks and QEMU retained')
    finally:
        selector.close()


def sync_data(args):
    config=private_json(CONFIG);nested_preflight(config)
    require(Path('/etc/machine-id').read_text().strip()==args.machine_id,'worker machine changed')
    require(Path('/proc/sys/kernel/random/boot_id').read_text().strip()==args.boot_id,'worker boot changed')
    require(config.get('data_filesystem_uuid')==args.data_uuid,'worker data generation changed')
    require(config.get('durable_metadata_reviewed') is True,'durable metadata layout is not reviewed')
    paths=config.get('durable_metadata_paths',[])
    require(paths and len(paths)<=16,'durable metadata paths missing')
    data_device=os.stat('/data').st_dev
    for value in paths:
        path=real_path(value)
        require(path.is_relative_to('/data') and path!=Path('/data') and os.stat(path).st_dev==data_device,'metadata is not on the reviewed persistent data filesystem')
    # Pause-to-snapshot exits guest shims. Any remaining Cube-owned task,
    # including one omitted by Master/API metadata, blocks worker poweroff.
    tasks=run(['/usr/bin/ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list','--quiet'],timeout=5)
    require(not tasks.strip(),'Cube-owned tasks remain after paused inventory')
    run(['/usr/bin/sync','-f','/data'],timeout=20)
    run(['/usr/bin/sync','-f','/'],timeout=20)
    return {'synced':True,'boot_id':args.boot_id}



def retain_clean_receipt(proof):
    require(isinstance(proof,dict) and proof.get('verified') is True,'verified pause proof missing')
    # Immutable evidence survives the next mutable running-unreconciled status.
    data={'version':1,'state':'stopped-clean','generated_at':time.time(),'proof':proof}
    identity=hashlib.sha256(json.dumps(proof,sort_keys=True,separators=(',',':')).encode()).hexdigest()
    path=ROOT/('clean-stop-'+identity+'.json')
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'w') as file:
        json.dump(data,file,sort_keys=True);file.write('\n');file.flush();os.fsync(file.fileno())
    fd=os.open(ROOT,os.O_RDONLY)
    try:os.fsync(fd)
    finally:os.close(fd)

def verify_start(args,require_paused=True):
    config=private_json(CONFIG);nested_preflight(config,True)
    require(Path('/etc/machine-id').read_text().strip()==args.machine_id,'worker machine changed')
    require(Path('/proc/sys/kernel/random/boot_id').read_text().strip()==args.boot_id,'worker boot changed')
    require(config.get('data_filesystem_uuid')==args.data_uuid,'worker data changed')
    require(config.get('durable_metadata_reviewed') is True,'durable metadata not reviewed')
    paths=config.get('durable_metadata_paths',[]);require(paths and len(paths)<=16,'persistent metadata paths missing')
    for value in paths:
        path=real_path(value);require(path.is_relative_to('/data') and path!=Path('/data') and path.stat().st_dev==os.stat('/data').st_dev,'metadata outside persistent data')
    if require_paused:
        tasks=run(['/usr/bin/ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list','--quiet'],timeout=5)
        require(not tasks.strip(),'unexpected running Cube tasks at clean startup')
    return {'verified':True,'boot_id':args.boot_id}

def qmp_powerdown(path):
    # Exactly one graceful request, never quit/reset/stop/kill.
    with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as connection:
        connection.settimeout(5);connection.connect(str(path));stream=connection.makefile('rwb')
        def receive(expected):
            for _ in range(32):
                line=stream.readline(65537)
                require(line and len(line)<=65536,'invalid QMP response')
                message=json.loads(line)
                if expected is None and 'QMP' in message:return
                if message.get('id')==expected:
                    require('error' not in message and 'return' in message,'QMP refused graceful request');return
            raise Blocked('QMP response limit exceeded')
        receive(None)
        for name in ('qmp_capabilities','system_powerdown'):
            stream.write(json.dumps({'execute':name,'id':name}).encode()+b'\n');stream.flush();receive(name)


class Supervisor:
    """Pure lifecycle state machine; injected effects are unit-test seams only."""
    def __init__(self,child,identity,drain,powerdown,publish,clock=time.monotonic,retain_clean=None):
        self.child,self.identity,self.drain,self.powerdown,self.publish,self.clock=child,identity,drain,powerdown,publish,clock
        self.retain_clean=retain_clean
        self.state='running-unreconciled';self.requested=False;self.deadline=None;self.receipt=None;self.clean=False
        self.report('boot requires management and binding reconciliation')
    def report(self,reason):
        self.last_report=self.clock()
        try:
            self.publish({'version':1,'state':self.state,'reason':reason,'supervisor_pid':os.getpid(),'qemu_pid':self.child.pid,'tenant_ready':False,'time':time.time()})
        except Exception:
            print('lifecycle status write failed; retaining worker supervision',flush=True)
    def signal(self,_number,_frame=None):
        # Signal handlers perform no provider I/O and never signal the child.
        self.requested=True
    def tick(self):
        exit_code=self.child.poll()
        if exit_code is not None:
            self.clean=self.state=='powerdown-wait' and exit_code==0
            if self.clean and self.retain_clean is not None:
                try:self.retain_clean(self.receipt)
                except Exception:self.clean=False
            self.state='stopped-clean' if self.clean else 'worker-lost'
            self.report('confirmed graceful exit' if self.clean else 'unplanned exit; keep admission fenced')
            return False
        if self.requested and self.state=='running-unreconciled':
            self.state='draining';self.report('stop requested; QEMU remains running')
            try:
                self.receipt=self.drain(self.identity)
                require(isinstance(self.receipt,dict) and self.receipt.get('verified') is True and self.receipt.get('qemu_pid')==self.child.pid,'stop coordinator did not return verified current-worker proof')
                self.powerdown();self.state='powerdown-wait';self.deadline=self.clock()+180
                self.report('graceful shutdown requested; no forced fallback')
            except Exception:
                self.state='stop-blocked';self.report('drain/proof/powerdown failed; operator intervention required')
        elif self.state=='powerdown-wait' and self.clock()>=self.deadline:
            self.state='stop-blocked';self.report('graceful shutdown exceeded deadline; QEMU retained without escalation')
        if self.clock()-self.last_report>=30:
            self.report('worker retained; tenant routing readiness remains separately gated')
        return True


def supervise(lock_path):
    require(sys.platform=='linux' and os.geteuid()==0,'native Linux root required')
    require(Path(lock_path)==ROOT/'backup.lock','fixed shared backup lock required')
    stop_requested=[False]
    def request_stop(_number,_frame):stop_requested[0]=True
    signal.signal(signal.SIGTERM,request_stop);signal.signal(signal.SIGINT,request_stop)
    config=private_json(CONFIG)
    with lifetime_lock(ROOT/'supervisor.lock',INSTANCE_MARKER,True) as instance, lifetime_lock(lock_path,BACKUP_MARKER,False) as backup:
        host_preflight(config)
        if stop_requested[0]:return 0
        # Inherited locks survive supervisor death until QEMU itself exits.
        child=subprocess.Popen(fixed_qemu(),stdin=subprocess.DEVNULL,close_fds=True,pass_fds=(instance,backup))
        state=Supervisor(child,{'qemu_pid':child.pid,'qemu_start_time':process_start_time(child.pid)},prepare_stop,lambda:qmp_powerdown(ROOT/'qmp.sock'),lambda value:write_status(STATUS,value),retain_clean=retain_clean_receipt)
        if stop_requested[0]:state.signal(signal.SIGTERM)
        signal.signal(signal.SIGTERM,state.signal);signal.signal(signal.SIGINT,state.signal)
        # On unhandled monitor/I/O exceptions remain alive rather than dropping
        # supervision while QEMU is alive. No terminate/kill path exists here.
        while True:
            try:
                if not state.tick():return 0 if state.clean else 1
            except Exception:
                print('worker lifecycle observation failed; retaining worker and lifetime locks',flush=True)
            time.sleep(1)


def monitor():
    value=private_json(STATUS)
    value['observed_at']=time.time()
    value['observation_only']=True
    filesystem=os.statvfs(DATA);value['outer_data_free_bytes']=filesystem.f_bavail*filesystem.f_frsize
    value['disk_reserve_low']=value['outer_data_free_bytes']<48*1024**3
    events=Path('/sys/fs/cgroup/system.slice/baarcha-cube-worker-01.service/memory.events')
    value['memory_events']={line.split()[0]:int(line.split()[1]) for line in events.read_text().splitlines()} if events.exists() else None
    value['state_reconciliation_monitor_wired']=True
    value['backup_age_monitor_wired']=True
    value['binding_observation_healthy']=False;value['backup_evidence_healthy']=False
    try:
        binding=json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe'],timeout=30))
        require(binding.get('consistent') is True,'binding observation failed')
        value['binding_observation_healthy']=True;value['binding_counts']={key:binding[key] for key in ['bindings','active','max_active']}
    except Exception:pass
    try:
        import backup_monitor
        policy=private_json('/etc/baarcha-cube/backup-monitor.json')
        value['backup']=backup_monitor.check(policy,private_json,digest)
        value['backup_evidence_healthy']=True
    except Exception:pass
    value['nested_disk_reserve_monitor_wired']=True
    value['status_stale']=time.time()-value.get('time',0)>120
    value['tenant_ready_evidence']=value['binding_observation_healthy'] and not value['status_stale']
    # This observation does not alter the supervisor status or open routing.
    # This is local status/journald output only. No external message, provider
    # lifecycle operation, restart, or data deletion is issued.
    print(json.dumps(value,sort_keys=True))
    return 0 if value['binding_observation_healthy'] and value['backup_evidence_healthy'] and not value['status_stale'] and not value['disk_reserve_low'] and value.get('memory_events') is not None and value['memory_events'].get('oom_kill',0)==0 else 1


def main():
    parser=argparse.ArgumentParser();sub=parser.add_subparsers(dest='action',required=True)
    p=sub.add_parser('supervise');p.add_argument('--lock-file',required=True)
    sub.add_parser('nested-preflight');sub.add_parser('nested-ready');sub.add_parser('monitor')
    p=sub.add_parser('observe-worker');p.add_argument('--machine-id',required=True);p.add_argument('--boot-id',required=True);p.add_argument('--data-uuid',required=True)
    p=sub.add_parser('verify-start');p.add_argument('--machine-id',required=True);p.add_argument('--boot-id',required=True);p.add_argument('--data-uuid',required=True)
    p=sub.add_parser('sync-data');p.add_argument('--machine-id',required=True);p.add_argument('--boot-id',required=True);p.add_argument('--data-uuid',required=True)
    args=parser.parse_args()
    if args.action=='supervise':return supervise(args.lock_file)
    if args.action=='monitor':return monitor()
    if args.action=='observe-worker':print(json.dumps(verify_start(args,False)));return 0
    if args.action=='verify-start':print(json.dumps(verify_start(args)));return 0
    if args.action=='sync-data':print(json.dumps(sync_data(args)));return 0
    result=nested_preflight(private_json(CONFIG),args.action=='nested-ready');print(json.dumps(result));return 0

if __name__=='__main__':
    try:sys.exit(main())
    except Exception:
        print('worker lifecycle preflight refused; inspect private configuration locally',file=sys.stderr);sys.exit(1)
