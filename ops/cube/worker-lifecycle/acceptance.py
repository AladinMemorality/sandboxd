#!/usr/bin/env python3
"""Reviewed diskless, networkless transient-unit fixture. Never the Cube worker."""
import argparse
import fcntl
import importlib.util
import json
import os
import re
from pathlib import Path
import signal
import socket
import stat
import subprocess
import sys
import time
import uuid

spec=importlib.util.spec_from_file_location('lifecycle',Path(__file__).with_name('lifecycle.py'))
life=importlib.util.module_from_spec(spec);spec.loader.exec_module(life)
PREFIX='baarcha-cube-lifecycle-fixture-'
BASE=Path('/opt/baarcha-bench')

def run(args,timeout=5):
    return subprocess.run(args,check=True,capture_output=True,timeout=timeout).stdout.decode()

def arguments(directory,name):
    return ['/usr/bin/qemu-system-x86_64','-name',name,'-nodefaults','-machine','q35,accel=tcg',
        '-m','32','-smp','1','-display','none','-nic','none','-monitor','none',
        '-qmp',f'unix:{directory}/qmp.sock,server=on,wait=off']

def child(directory,name,case):
    life.require(re.fullmatch(PREFIX+r'[a-f0-9]{12}',name) and directory==BASE/name/case and directory.resolve()==directory,'exact private fixture scope required')
    deadline=time.monotonic()+45
    with life.lifetime_lock(directory/'instance.lock',life.INSTANCE_MARKER,True) as instance,life.lifetime_lock(directory/'backup.lock',life.BACKUP_MARKER,False) as backup:
        process=subprocess.Popen(arguments(directory,name),stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=open(directory/'qemu.log','wb'),pass_fds=(instance,backup))
        def drain(identity):
            if case=='failed-drain':raise life.Blocked('synthetic active writer')
            return {'verified':True,'qemu_pid':identity['qemu_pid']}
        supervisor=life.Supervisor(process,{'qemu_pid':process.pid},drain,lambda:life.qmp_powerdown(directory/'qmp.sock'),lambda value:life.write_status(directory/'status.json',value))
        signal.signal(signal.SIGTERM,supervisor.signal);signal.signal(signal.SIGINT,supervisor.signal)
        life.write_status(directory/'signals-ready.json',{'ready':True})
        while supervisor.tick():
            if time.monotonic()>=deadline:
                exact_qemu(process.pid,directory,name)
                life.require(qmp(directory,'query-name').get('name')==name,'deadline cleanup fixture identity mismatch')
                qmp(directory,'quit')
                deadline=float('inf')
            time.sleep(.1)
        return 0 if supervisor.clean else 1

def qmp(directory,command):
    path=directory/'qmp.sock';info=path.lstat()
    life.require(stat.S_ISSOCK(info.st_mode) and info.st_uid==0,'unexpected fixture socket')
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(3);connection.connect(str(path));file=connection.makefile('rwb')
        def read(identity):
            for _ in range(20):
                raw=file.readline(65537);life.require(raw and len(raw)<=65536,'fixture QMP response invalid');value=json.loads(raw)
                if identity is None and 'QMP' in value:return value
                if value.get('id')==identity:
                    life.require('error' not in value,'fixture QMP error');return value.get('return')
            raise RuntimeError('fixture QMP bounded response failed')
        read(None)
        for operation in ['qmp_capabilities',command]:
            file.write(json.dumps({'execute':operation,'id':operation}).encode()+b'\n');file.flush();result=read(operation)
        return result

def info(unit):
    result=subprocess.run(['/usr/bin/systemctl','show',unit,'-p','LoadState','-p','ActiveState','-p','SubState','-p','MainPID','-p','KillMode','-p','SendSIGKILL','-p','Restart','-p','TimeoutStopUSec','-p','ControlGroup'],capture_output=True,timeout=5)
    values=dict(line.split('=',1) for line in result.stdout.decode().splitlines() if '=' in line)
    if values.get('LoadState')=='not-found' and unit.startswith(PREFIX):return {'MainPID':'0','ActiveState':'inactive','SubState':'dead'}
    life.require(result.returncode==0,'fixture unit inspection failed')
    return values

def exact_qemu(pid,directory,name):
    raw=Path(f'/proc/{pid}/cmdline').read_bytes().split(b'\x00')
    life.require([x.decode() for x in raw if x]==arguments(directory,name),'fixture QEMU identity mismatch')

def lock_refused(path,marker):
    try:
        with life.lifetime_lock(path,marker,True):pass
    except BlockingIOError:return True
    return False

def wait(predicate,seconds):
    deadline=time.monotonic()+seconds
    while time.monotonic()<deadline:
        if predicate():return
        time.sleep(.1)
    raise RuntimeError('bounded fixture observation timed out')

def execute():
    life.require(os.geteuid()==0 and sys.platform=='linux','outer Linux root fixture only')
    started=time.monotonic();name=PREFIX+uuid.uuid4().hex[:12];base=BASE/name;base.mkdir(mode=0o700)
    results=[]
    for case in ['failed-drain','unhandled-powerdown']:
        life.require(time.monotonic()-started<40,'fixture total execution budget nearly exhausted')
        directory=base/case;directory.mkdir(mode=0o700);unit=name+'-'+case+'.service';qemu_pid=None;row={'case':case,'unit':unit,'cleanup_verified':False}
        try:
            run(['/usr/bin/systemd-run','--unit',unit,'--collect','--no-block',
                 '--property=User=root','--property=UMask=0077','--property=CPUQuota=100%',
                 '--property=MemoryMax=256M','--property=MemorySwapMax=0',
                 '--property=KillMode=process','--property=SendSIGKILL=no',
                 '--property=TimeoutStopSec=infinity','--property=Restart=no',
                 '/usr/bin/python3',str(Path(__file__).resolve()),'child','--directory',str(directory),'--name',name,'--case',case])
            wait(lambda:(directory/'status.json').exists() and (directory/'qmp.sock').exists() and (directory/'signals-ready.json').exists(),8)
            status=json.loads((directory/'status.json').read_text());qemu_pid=status['qemu_pid'];exact_qemu(qemu_pid,directory,name)
            before=info(unit);life.require(before['MainPID']==str(status['supervisor_pid']),'wrong transient main PID')
            life.require(before['KillMode']=='process' and before['SendSIGKILL']=='no' and before['TimeoutStopUSec']=='infinity' and before['Restart']=='no','unit differs from reviewed signal settings')
            held=[str((Path(f'/proc/{qemu_pid}/fd')/entry.name).resolve()) for entry in Path(f'/proc/{qemu_pid}/fd').iterdir()]
            row['qemu_inherited_both_locks']=str(directory/'instance.lock') in held and str(directory/'backup.lock') in held
            life.require(row['qemu_inherited_both_locks'],'QEMU did not retain inherited lock FDs')
            run(['/usr/bin/systemctl','stop','--no-block',unit])
            expected='stop-blocked' if case=='failed-drain' else 'powerdown-wait'
            wait(lambda:json.loads((directory/'status.json').read_text()).get('state')==expected,5)
            time.sleep(2)
            after=info(unit);exact_qemu(qemu_pid,directory,name)
            life.require(after['MainPID']==before['MainPID'] and after['ActiveState']=='deactivating','ordinary stop lost supervisor or restarted it')
            life.require(lock_refused(directory/'backup.lock',life.BACKUP_MARKER),'cold capture acquired live backup lock')
            life.require(lock_refused(directory/'instance.lock',life.INSTANCE_MARKER),'second supervisor acquired instance lock')
            memory=Path('/sys/fs/cgroup')/after['ControlGroup'].lstrip('/')/'memory.events'
            events={line.split()[0]:int(line.split()[1]) for line in memory.read_text().splitlines()}
            life.require(events.get('oom',0)==0 and events.get('oom_kill',0)==0,'fixture OOM')
            row.update({'passed':True,'observed_state':expected,'qemu_alive_after_stop':True,'supervisor_same_after_stop':True,'backup_exclusive_refused':True,'second_instance_refused':True,'memory_events':events})
        finally:
            # Cleanup is restricted to the exact diskless fixture. No unit kill,
            # OS signal escalation, generic pkill, or worker/rescue operation.
            if qemu_pid is not None and Path(f'/proc/{qemu_pid}').exists():
                exact_qemu(qemu_pid,directory,name);life.require(qmp(directory,'query-name').get('name')==name,'cleanup QMP identity mismatch');qmp(directory,'quit')
                wait(lambda:not Path(f'/proc/{qemu_pid}').exists(),5)
            elif (directory/'qmp.sock').exists():
                life.require(qmp(directory,'query-name').get('name')==name,'fallback cleanup identity mismatch');qmp(directory,'quit')
            wait(lambda:info(unit).get('MainPID','0')=='0',5)
            # Transient --collect may already have removed a failed unit.
            subprocess.run(['/usr/bin/systemctl','reset-failed',unit],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=3)
            life.require(not lock_refused(directory/'backup.lock',life.BACKUP_MARKER),'fixture backup lock leaked')
            life.require(not lock_refused(directory/'instance.lock',life.INSTANCE_MARKER),'fixture instance lock leaked')
            row['cleanup_verified']=True;row['cleanup_method']='QMP quit of exact diskless fixture only';results.append(row)
            life.write_status(base/'result.json',{'version':1,'source':'exact candidate Supervisor and lifetime locks','cases':results,'elapsed_seconds':time.monotonic()-started,'real_guest_os_clean_shutdown_verified':False,'production_changes':False})
    life.require(time.monotonic()-started<60,'fixture exceeded reviewed time bound')
    print(json.dumps({'evidence_directory':str(base),'cases':len(results),'all_passed':all(x.get('passed') and x['cleanup_verified'] for x in results),'elapsed_seconds':time.monotonic()-started}))

if __name__=='__main__':
    parser=argparse.ArgumentParser();sub=parser.add_subparsers(dest='action',required=True);sub.add_parser('run');p=sub.add_parser('child');p.add_argument('--directory',type=Path,required=True);p.add_argument('--name',required=True);p.add_argument('--case',choices=['failed-drain','unhandled-powerdown'],required=True);args=parser.parse_args()
    if args.action=='run':execute()
    else:sys.exit(child(args.directory,args.name,args.case))
