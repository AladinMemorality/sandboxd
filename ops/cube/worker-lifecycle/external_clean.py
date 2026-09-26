#!/usr/bin/env python3
"""Explicit external clean-exit evidence for an unretryable stop-blocked worker.

Library called by the fenced maintenance runner. No signal is ever sent to the
supervisor or QEMU. Only a newly owned strace witness can be detached on failure.
This does not manufacture the supervisor's own clean receipt or change its state.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import socket
import stat
import struct
import subprocess
import time

METHOD='qmp-guest-shutdown+supervisor-wait4'
LIMIT=2*1024*1024

class Refused(RuntimeError): pass

def need(ok,message):
    if not ok: raise Refused(message)

def canonical(value):return (json.dumps(value,sort_keys=True,separators=(',',':'))+'\n').encode()
def sha(raw):return hashlib.sha256(raw).hexdigest()
def ticks(pid):return Path('/proc/'+str(pid)+'/stat').read_text().rsplit(')',1)[1].split()[19]

def publish(path,value):
    raw=value if isinstance(value,bytes) else canonical(value)
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'wb') as file:file.write(raw);file.flush();os.fsync(file.fileno())
    fd=os.open(Path(path).parent,os.O_DIRECTORY|os.O_RDONLY|os.O_NOFOLLOW)
    try:os.fsync(fd)
    finally:os.close(fd)

def parse_wait4(raw,supervisor_pid,qemu_pid):
    """Fail closed on unsupported/unfinished relevant syscall formatting.

    CPython Popen.poll uses waitpid(WNOHANG), observed as wait4 on Linux. A zero
    return, ECHILD, a signalled child, or another PID never establishes clean exit.
    Optional strace thread prefixes must identify the original supervisor.
    """
    need(len(raw)<=LIMIT,'wait trace exceeds bound')
    need(not raw or raw.endswith(b'\n'),'incomplete final trace line')
    text=raw.decode('ascii'); polls=[]; exits=[]
    line_pattern=re.compile(r'(?:(?:\[pid\s+(\d+)\]|(\d+))\s+)?(\d+\.\d+)\s+(.*)\Z')
    for i,line in enumerate(text.splitlines()):
        if not line.strip():continue
        match=line_pattern.fullmatch(line)
        need(match is not None,'unsupported wait witness line')
        thread=match[1] or match[2]
        need(thread is None or int(thread)==supervisor_pid,'wait witness belongs to another thread/process')
        timestamp=float(match[3]); call=match[4]
        need('waitid(' not in call and '<unfinished ...>' not in call and 'resumed>' not in call,'unsupported or incomplete wait syscall')
        other=re.fullmatch(r'wait4\(([1-9][0-9]*), (.*), WNOHANG, NULL\)\s+=\s+(-?\d+)(?:\s+.*)?',call)
        if other is not None and int(other[1])!=qemu_pid:
            # Popen finalization can reap an earlier failed stop helper after
            # QEMU exits. Preserve that line, but never use it as QEMU proof.
            need(bool(exits) and timestamp>=exits[0]['at'] and int(other[3]) in (0,int(other[1])),'unrelated child before exact QEMU completion')
            continue
        result=re.fullmatch(r'wait4\('+str(qemu_pid)+r', (.*), WNOHANG, NULL\)\s+=\s+(-?\d+)(?:\s+.*)?',call)
        need(result is not None,'unexpected syscall or child in wait witness')
        if int(result[2])==0:
            polls.append({'line':i,'at':timestamp});continue
        need(int(result[2])==qemu_pid and result[1]=='[{WIFEXITED(s) && WEXITSTATUS(s) == 0}]','child did not exit normally with status0')
        exits.append({'line':i,'at':timestamp,'exit_code':0})
    need(len(exits)<=1,'duplicate child completion')
    return {'polls':polls,'exits':exits}

def complete_trace(raw):
    """Polling ignores only a not-yet-terminated tail; final evidence is strict."""
    need(len(raw)<=LIMIT,'wait trace exceeds bound')
    if not raw or raw.endswith(b'\n'):return raw
    prefix,separator,_=raw.rpartition(b'\n')
    return prefix+separator

def validate_witness(identity,pause,retained,actions,trace,status):
    need(identity['version']==1 and identity['method']==METHOD,'unsupported external witness')
    for key in ('worker_machine_id','worker_boot_id','data_uuid','qemu_pid','qemu_start_time'):
        need(pause[key]==identity[key],'pause proof generation differs')
    need(pause['version']==1 and pause['verified'] is True and pause['provider_jobs']==0 and all(v=='paused' for v in pause['guest_states'].values()),'complete paused proof required')
    need(retained.get('version')==1 and retained.get('stopped') is True and retained.get('boot_id')==identity['worker_boot_id'],'actual retained management shutdown required')
    parsed=parse_wait4(trace,identity['supervisor_pid'],identity['qemu_pid'])
    need(len(parsed['exits'])==1 and parsed['polls'],'witness must be attached before the child exits')
    recovered=actions.get('version')==2
    fields={'version','qmp_socket_inode','peer_pid','finished_at','messages'}
    fields|={'first_poll_at','powerdown_event_at','partial_sha256'} if recovered else {'attached_at','sent_at'}
    need(set(actions)==fields,'unexpected QMP evidence fields')
    need(actions['version'] in (1,2) and actions['peer_pid']==identity['qemu_pid'] and actions['qmp_socket_inode']==identity['qmp_socket_inode'],'QMP peer/socket identity differs')
    if recovered:
        # A failed final parser left raw QMP messages, but not local attach/send
        # timestamps. Use the actual first poll and QEMU POWERDOWN event, and
        # explicitly identify this recovered evidence instead of inventing them.
        need(re.fullmatch(r'[a-f0-9]{64}',actions['partial_sha256']) is not None,'partial evidence hash required')
        need(actions['first_poll_at']==parsed['polls'][0]['at'],'first poll differs')
        power=[v['message'] for v in actions['messages'] if v['direction']=='receive' and v['message'].get('event')=='POWERDOWN']
        need(len(power)==1,'one actual POWERDOWN event required')
        ts=power[0].get('timestamp',{})
        need(type(ts.get('seconds')) is int and type(ts.get('microseconds')) is int and 0<=ts['microseconds']<1000000,'POWERDOWN timestamp missing')
        sent=ts['seconds']+ts['microseconds']/1e6
        need(actions['powerdown_event_at']==sent and actions['first_poll_at']<=sent<=parsed['exits'][0]['at']<=actions['finished_at'],'recovered witness chronology differs')
    else:
        sent=actions['sent_at']
        need(actions['attached_at']<=parsed['polls'][0]['at']<=sent<=parsed['exits'][0]['at']<=actions['finished_at'],'witness/action chronology differs')
    requests=[(i,v) for i,v in enumerate(actions['messages']) if v['direction']=='send' and v['message'].get('execute')=='system_powerdown']
    need(len(requests)==1,'exactly one graceful QMP power request required')
    need(all(v['message'].get('execute') in ('qmp_capabilities','query-name','query-status','system_powerdown') for v in actions['messages'] if v['direction']=='send'),'unreviewed QMP action')
    events=[(i,v) for i,v in enumerate(actions['messages']) if v['direction']=='receive' and v['message'].get('event')=='SHUTDOWN']
    need(len(events)==1 and events[0][0]>requests[0][0],'exact shutdown event after request required')
    if recovered:
        power_index=next(i for i,v in enumerate(actions['messages']) if v['direction']=='receive' and v['message'].get('event')=='POWERDOWN')
        need(requests[0][0]<power_index<events[0][0],'POWERDOWN must follow the request and precede guest shutdown')
    event=events[0][1]['message'];need(event.get('data')=={'guest':True,'reason':'guest-shutdown'},'host quit/reset is not guest shutdown')
    ts=event.get('timestamp',{});need(type(ts.get('seconds')) is int and type(ts.get('microseconds')) is int and 0<=ts['microseconds']<1000000,'QMP event timestamp absent')
    event_time=ts['seconds']+ts['microseconds']/1e6
    need(sent<=event_time<=parsed['exits'][0]['at']+.001,'guest event not correlated with wait completion')
    need(status.get('state')=='worker-lost' and status.get('qemu_pid')==identity['qemu_pid'] and status.get('supervisor_pid')==identity['supervisor_pid'],'retain the original supervisor loss classification')
    return {'version':1,'method':METHOD,'guest_shutdown':True,'qemu_exit_code':0,'supervisor_reported_state':'worker-lost','qemu_pid':identity['qemu_pid'],'qemu_start_time':identity['qemu_start_time'],'worker_boot_id':identity['worker_boot_id'],'outer_boot_id':identity['outer_boot_id']}

class WaitWitness:
    def __init__(self,identity,directory):self.identity=identity;self.directory=Path(directory);self.process=None;self.attached_at=None
    def same(self):
        for field in ('supervisor','qemu'):
            need(ticks(self.identity[field+'_pid'])==self.identity[field+'_start_time'],'process generation changed')
        need(Path('/proc/sys/kernel/random/boot_id').read_text().strip()==self.identity['outer_boot_id'],'outer boot changed')
    def attach(self):
        self.same();self.attached_at=time.time();path=self.directory/'wait4.trace'
        need(not path.exists(),'new witness output required')
        errfd=os.open(self.directory/'strace.stderr',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        self.stderr=os.fdopen(errfd,'wb')
        # No read/write/open/exec/environment/file contents are traced.
        self.process=subprocess.Popen(['/usr/bin/strace','-qq','-f','-ttt','-e','trace=wait4,waitid','-e','signal=none','-p',str(self.identity['supervisor_pid']),'-o',str(path)],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=self.stderr,umask=0o077)
        deadline=time.monotonic()+15
        while time.monotonic()<deadline:
            self.same();need(self.process.poll() is None,'wait witness exited before ready')
            if path.exists():
                parsed=parse_wait4(complete_trace(path.read_bytes()),self.identity['supervisor_pid'],self.identity['qemu_pid'])
                need(not parsed['exits'],'child exited before power request')
                if parsed['polls']:return
            time.sleep(.1)
        raise Refused('no actual wait4 readiness sample')
    def close(self):
        if self.process is not None and self.process.poll() is None:
            # Detach the process created here. No signal goes to traced targets.
            self.process.send_signal(signal.SIGINT)
            self.process.wait(timeout=10)
        if hasattr(self,'stderr'):self.stderr.close()

class QMP:
    def __init__(self,path,identity):
        self.identity=identity;self.messages=[];self.path=Path(path);self.deadline=time.monotonic()+15
        s=self.path.lstat();need(stat.S_ISSOCK(s.st_mode) and s.st_uid==0 and s.st_ino==identity['qmp_socket_inode'],'QMP socket changed')
        self.socket=socket.socket(socket.AF_UNIX);self.socket.settimeout(5);self.socket.connect(str(self.path))
        pid,uid,_=struct.unpack('3i',self.socket.getsockopt(socket.SOL_SOCKET,socket.SO_PEERCRED,12))
        need(pid==identity['qemu_pid'] and uid==0,'QMP peer is not the exact QEMU')
        self.file=self.socket.makefile('rwb');self.receive(greeting=True)
        self.command('qmp_capabilities');name=self.command('query-name')
        need(name=={'name':'baarcha-cube-worker-01'},'wrong QEMU worker name')
        need(self.command('query-status').get('running') is True,'worker QEMU is not running')
    def receive(self,greeting=False):
        left=self.deadline-time.monotonic();need(left>0,'overall QMP deadline exceeded');self.socket.settimeout(left)
        raw=self.file.readline(65537);need(raw and len(raw)<=65536 and len(self.messages)<256,'bounded QMP event missing')
        value=json.loads(raw);self.messages.append({'direction':'receive','message':value})
        if greeting:need('QMP' in value,'QMP greeting required')
        return value
    def command(self,name):
        need(name in ('qmp_capabilities','query-name','query-status','system_powerdown'),'unreviewed QMP command')
        value={'execute':name,'id':name};self.messages.append({'direction':'send','message':value});self.file.write(canonical(value));self.file.flush()
        for _ in range(32):
            message=self.receive()
            if message.get('id')==name:
                need('error' not in message and 'return' in message,'QMP command failed');return message['return']
        raise Refused('QMP command response absent')
    def shutdown_event(self):
        while not any(v['direction']=='receive' and v['message'].get('event')=='SHUTDOWN' for v in self.messages):self.receive()
    def close(self):self.file.close();self.socket.close()


def shutdown(identity,pause,retained,directory,fence):
    """Called only AFTER actual native pause and retained component shutdown.

    `fence` rechecks the maintenance caller's held locks, exact stopped controller,
    reviewed offline routing and both original process generations. A failed or
    timed out call retains original data and cannot mint a clean receipt.
    """
    d=Path(directory);need(d.is_absolute() and d.resolve(strict=True)==d,'canonical fresh evidence directory')
    need(d.stat().st_uid==0 and stat.S_IMODE(d.stat().st_mode)==0o700 and not any(d.iterdir()),'empty private witness directory required')
    for name,value in [('identity.json',identity),('pause.json',pause),('retained-stop.json',retained)]:publish(d/name,value)
    witness=WaitWitness(identity,d);qmp=None
    try:
        fence();witness.attach();qmp=QMP('/opt/baarcha-cube/worker-01/qmp.sock',identity)
        fence();witness.same();sent=time.time();qmp.deadline=time.monotonic()+180
        qmp.command('system_powerdown');qmp.shutdown_event()
        deadline=time.monotonic()+30
        while time.monotonic()<deadline:
            raw=(d/'wait4.trace').read_bytes();parsed=parse_wait4(complete_trace(raw),identity['supervisor_pid'],identity['qemu_pid'])
            if parsed['exits']:break
            need(witness.process.poll() is None,'witness ended without exact child completion');time.sleep(.1)
        else:raise Refused('child wait completion missing')
        need(not Path('/proc/'+str(identity['qemu_pid'])).exists(),'old QEMU PID remains or was reused')
        witness.process.wait(timeout=10)
        need(witness.process.returncode==0,'wait witness failed')
        raw=(d/'wait4.trace').read_bytes()
        actions={'version':1,'qmp_socket_inode':identity['qmp_socket_inode'],'peer_pid':identity['qemu_pid'],'attached_at':witness.attached_at,'sent_at':sent,'finished_at':time.time(),'messages':qmp.messages}
        status=json.loads(Path('/opt/baarcha-cube/worker-01/lifecycle-status.json').read_bytes())
        summary=validate_witness(identity,pause,retained,actions,raw,status)
        publish(d/'qmp.json',actions);publish(d/'supervisor-actual.json',status)
        # Hash every raw input; private trace/status are preserved, not rewritten.
        artifacts={name:sha((d/name).read_bytes()) for name in ('identity.json','pause.json','retained-stop.json','wait4.trace','strace.stderr','qmp.json','supervisor-actual.json')}
        manifest={'version':1,'method':METHOD,'validated':summary,'artifacts':artifacts};publish(d/'manifest.json',manifest)
        external={'version':1,'method':METHOD,'outer_boot_id':identity['outer_boot_id'],'supervisor_pid':identity['supervisor_pid'],'supervisor_start_time':identity['supervisor_start_time'],'qemu_pid':identity['qemu_pid'],'qemu_start_time':identity['qemu_start_time'],'worker_boot_id':identity['worker_boot_id'],'evidence_directory':str(d),'evidence_sha256':sha((d/'manifest.json').read_bytes())}
        receipt={'version':1,'state':'externally-stopped-clean','generated_at':time.time(),'proof':pause,'external':external};publish(d/'receipt.json',receipt)
        return receipt
    finally:
        if qmp is not None:
            if not (d/'qmp.json').exists():publish(d/'qmp.partial.json',{'version':1,'partial':True,'messages':qmp.messages})
            qmp.close()
        witness.close()


def private_bytes(path,limit=LIMIT):
    p=Path(path);need(p.is_absolute() and p.resolve(strict=True)==p,'canonical evidence file required')
    for ancestor in p.parents:
        info=ancestor.stat();need(info.st_uid==0 and not info.st_mode&0o022,'writable evidence ancestor')
    fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    with os.fdopen(fd,'rb') as file:
        info=os.fstat(file.fileno());need(stat.S_ISREG(info.st_mode) and info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o600 and info.st_nlink==1 and info.st_size<=limit,'root0600 bounded evidence required')
        raw=file.read(limit+1);need(len(raw)<=limit,'evidence exceeds bound');return raw

EVIDENCE_FILES=('identity.json','pause.json','retained-stop.json','wait4.trace','strace.stderr','qmp.json','supervisor-actual.json')

def validate_receipt(path,read=private_bytes):
    """Rehash the CLOSED evidence set, including after a later worker boot.

    Current lifecycle-status is deliberately not consulted: the original
    worker-lost record is preserved in the evidence closure. Returns only this
    finite closure so a backup finalizer never adopts arbitrary extra paths.
    """
    p=Path(path);need(p.name=='receipt.json','fixed external receipt filename required');raw=read(p);receipt=json.loads(raw)
    need(set(receipt)=={'version','state','generated_at','proof','external'} and receipt['version']==1 and receipt['state']=='externally-stopped-clean','explicit external receipt required')
    e=receipt['external'];expected={'version','method','outer_boot_id','supervisor_pid','supervisor_start_time','qemu_pid','qemu_start_time','worker_boot_id','evidence_directory','evidence_sha256'}
    need(set(e)==expected and e['version']==1 and e['method']==METHOD,'external receipt schema differs')
    d=Path(e['evidence_directory']);need(d==p.parent,'receipt must reside beside its exact evidence')
    manifest_raw=read(d/'manifest.json');need(sha(manifest_raw)==e['evidence_sha256'],'external manifest hash differs')
    manifest=json.loads(manifest_raw)
    names=set(EVIDENCE_FILES)
    actions=json.loads(read(d/'qmp.json'))
    if actions.get('version')==2:names.add('qmp.partial.json')
    need(set(manifest)=={'version','method','validated','artifacts'} and manifest['version']==1 and manifest['method']==METHOD and set(manifest['artifacts'])==names,'finite evidence closure required')
    files={name:read(d/name) for name in names}
    need(all(sha(value)==manifest['artifacts'][name] for name,value in files.items()),'external evidence changed')
    identity=json.loads(files['identity.json'])
    need(all(identity[key]==e[key] for key in ('outer_boot_id','supervisor_pid','supervisor_start_time','qemu_pid','qemu_start_time','worker_boot_id')),'external identity differs')
    pause=json.loads(files['pause.json']);need(pause==receipt['proof'],'external receipt differs from actual pause proof')
    actions=json.loads(files['qmp.json'])
    if actions.get('version')==2:
        partial=json.loads(files['qmp.partial.json'])
        need(set(partial)=={'version','partial','messages'} and partial['version']==1 and partial['partial'] is True and partial['messages']==actions['messages'] and sha(files['qmp.partial.json'])==actions['partial_sha256'],'recovered QMP messages differ from preserved partial evidence')
    summary=validate_witness(identity,pause,json.loads(files['retained-stop.json']),actions,files['wait4.trace'],json.loads(files['supervisor-actual.json']))
    need(summary==manifest['validated'] and actions['finished_at']<=receipt['generated_at']<=time.time()+1,'external validation/receipt chronology differs')
    closure={**files,'manifest.json':manifest_raw,p.name:raw}
    return {'receipt':receipt,'summary':summary,'closure':[{'path':str(d/name),'sha256':sha(value),'bytes':len(value)} for name,value in sorted(closure.items())]}


def verify_main():
    import argparse
    p=argparse.ArgumentParser();p.add_argument('--verify',type=Path,required=True);args=p.parse_args()
    need(os.geteuid()==0,'native root evidence verification required')
    value=validate_receipt(args.verify)
    print(json.dumps({'version':1,'verified':True,'receipt_sha256':sha(private_bytes(args.verify)),'method':METHOD,'qemu_pid':value['receipt']['proof']['qemu_pid'],'qemu_start_time':value['receipt']['proof']['qemu_start_time'],'worker_boot_id':value['receipt']['proof']['worker_boot_id']},sort_keys=True))


# Fixed one-use authorization for the NEXT supervisor. This does not update the
# old supervisor's actual status and never authorizes resuming stale guest RAM.
START_AUTH=Path('/etc/baarcha-cube/external-clean-start.json')
START_HELPER=Path('/usr/local/libexec/baarcha-cube-external-clean.py')
WORKER=Path('/opt/baarcha-cube/worker-01')
STOP_CONFIG=Path('/etc/baarcha-cube/worker-stop.json')
EXPECTED_DISKS=(WORKER/'root.qcow2',Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2'),WORKER/'seed.img')

def artifact_sha(path):
    p=Path(path);need(p.resolve(strict=True)==p,'canonical verifier required')
    for ancestor in p.parents:
        info=ancestor.stat();need(info.st_uid==0 and not info.st_mode&0o022,'writable verifier ancestor')
    info=p.stat();need(stat.S_ISREG(info.st_mode) and info.st_uid==0 and not info.st_mode&0o022 and info.st_size<=131072,'untrusted fixed verifier')
    return sha(p.read_bytes())

def validate_start_authorization(read=private_bytes,process_exists=lambda pid:Path('/proc/'+str(pid)).exists()):
    auth_raw=read(START_AUTH);auth=json.loads(auth_raw)
    need(set(auth)=={'version','purpose','receipt','receipt_sha256','actual_status_sha256','stop_config_sha256','verifier_sha256','disk_identities'} and auth['version']==1 and auth['purpose']=='external-clean-one-use-start','explicit one-use external start authorization required')
    need(artifact_sha(START_HELPER)==auth['verifier_sha256'],'authorized verifier changed')
    need(sha(read(auth['receipt']))==auth['receipt_sha256'],'authorized receipt changed')
    status_raw=read(WORKER/'lifecycle-status.json');need(sha(status_raw)==auth['actual_status_sha256'],'actual prior supervisor status changed')
    stop_raw=read(STOP_CONFIG);need(sha(stop_raw)==auth['stop_config_sha256'],'pre-start stop configuration changed')
    value=validate_receipt(auth['receipt'],read);receipt=value['receipt'];status=json.loads(status_raw);stop=json.loads(stop_raw)
    original=json.loads(read(Path(receipt['external']['evidence_directory'])/'supervisor-actual.json'))
    need(status==original and status['state']=='worker-lost','preserved actual supervisor loss record required')
    proof=receipt['proof']
    for k in ('worker_machine_id','worker_boot_id','data_uuid','qemu_pid','qemu_start_time'):
        need(stop[k]==proof[k],'authorized stop generation differs')
    need(not process_exists(proof['qemu_pid']) and not process_exists(receipt['external']['supervisor_pid']),'old worker or supervisor still exists')
    expected=set(map(str,EXPECTED_DISKS))
    need(set(auth['disk_identities'])==expected,'exact retained three disks required')
    for path,identity in auth['disk_identities'].items():
        p=Path(path);need(p.resolve(strict=True)==p,'disk path changed')
        info=p.stat();need(stat.S_ISREG(info.st_mode) and info.st_uid==0 and {'device':info.st_dev,'inode':info.st_ino,'size':info.st_size,'mtime_ns':info.st_mtime_ns}==identity,'disk changed after external authorization')
    return {'authorization_sha256':sha(auth_raw),'receipt_sha256':auth['receipt_sha256'],'source':str(START_AUTH),'consumed':str(WORKER/('external-start-consumed-'+auth['receipt_sha256']+'.json'))}

def consume_start_authorization(value):
    need(sha(private_bytes(START_AUTH))==value['authorization_sha256'],'external authorization changed before consumption')
    target=Path(value['consumed']);need(not target.exists() and target.parent==WORKER,'external authorization already consumed')
    # Hardlink+unlink cannot overwrite an existing receipt; both directories are
    # on the root filesystem. A crash between steps is safely non-replayable.
    os.link(START_AUTH,target,follow_symlinks=False)
    fd=os.open(target.parent,os.O_DIRECTORY|os.O_RDONLY|os.O_NOFOLLOW)
    try:os.fsync(fd)
    finally:os.close(fd)
    START_AUTH.unlink()
    for parent in (START_AUTH.parent,target.parent):
        fd=os.open(parent,os.O_DIRECTORY|os.O_RDONLY|os.O_NOFOLLOW)
        try:os.fsync(fd)
        finally:os.close(fd)

if __name__=='__main__':
    try:verify_main()
    except (Refused,OSError,ValueError,KeyError,TypeError,subprocess.SubprocessError):
        raise SystemExit('External clean-stop evidence refused; no lifecycle state changed.')
