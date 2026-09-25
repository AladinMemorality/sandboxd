#!/usr/bin/env python3
"""Read installed nested identities and render private candidate files only.
No installation, service changes, container changes or permission-file creation.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import stat
import subprocess
import sys
import tomllib

TOOLBOX=Path('/usr/local/services/cubetoolbox')
SCRIPT='/usr/local/libexec/baarcha-cube-worker-lifecycle.py'
META=Path('/data/cubelet/persistent-metadata')
NATIVE={'cubelet':'Cubelet/bin/cubelet','cubemaster':'CubeMaster/bin/cubemaster',
        'cube-api':'CubeAPI/bin/cube-api','cubeops':'CubeOps/bin/cubeops',
        'cube-templatecenter':'CubeTemplateCenter/bin/templatecenter'}
SERVICES=('mysql','redis','minio','coredns','dns','cubeops','cubemaster','cube-api',
          'cubelet','cube-templatecenter','cube-lifecycle-manager','cube-proxy',
          'cube-egress-net','cube-egress','webui','s3lvol')
PLUGINS={
 'io.cubelet.cubestore.v1.cubebox':('root_path','cubebox'),
 'io.cubelet.cubemetastore.v1.mutilmeta':('root_path','multimeta'),
 'io.cubelet.internal.v1.storage':('root_path','storage'),
 'io.cubelet.internal.v1.volume':('root_path','volume'),
 'io.cubelet.internal.v1.cgroup':('root_path','cgroup'),
 'io.cubelet.internal.v1.network':('root_path','network'),
 'io.cubelet.internal.v1.netfile':('root_path','netfile'),
 'io.cubelet.internal.v1.images':('state_path','images'),
 'io.cubelet.internal.v1.cleanup':('root_path','cleanup'),
 'io.containerd.metadata.v1.bolt':('root_path','containerd'),
 'io.containerd.snapshotter.v1.overlayfs':('root_path','overlayfs')}
STATEFUL={'cube-sandbox-mysql':'/var/lib/mysql','cube-sandbox-redis':'/data',
          'cube-sandbox-minio':'/data','cube-production-registry':'/var/lib/registry'}
SHA=re.compile(r'^[a-f0-9]{64}$')

def need(ok,message):
    if not ok:raise RuntimeError(message)

def run(args):
    return subprocess.check_output(args,stderr=subprocess.DEVNULL,timeout=10).decode().strip()

def real(path):
    path=Path(path)
    need(path.is_absolute() and path.resolve(strict=True)==path,'canonical existing path required')
    return path

def digest(path):
    path=real(path);info=path.stat()
    need(stat.S_ISREG(info.st_mode) and info.st_uid==0 and not info.st_mode&0o022,'root-owned immutable input required')
    h=hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda:f.read(1024*1024),b''):h.update(block)
    return h.hexdigest()

def unit(name):
    raw=run(['/usr/bin/systemctl','show',name,'-p','Id','-p','LoadState','-p','ActiveState','-p','MainPID','-p','Type','-p','FragmentPath','-p','DropInPaths'])
    data=dict(line.split('=',1) for line in raw.splitlines() if '=' in line)
    need(data.get('Id')==name and data.get('LoadState')=='loaded','expected installed unit missing')
    return data

def start_time(pid):
    text=Path(f'/proc/{pid}/stat').read_text();return text[text.rfind(')')+1:].split()[19]

def collect():
    need(sys.platform=='linux' and os.geteuid()==0 and socket.gethostname()=='baarcha-cube-worker-01','actual nested Linux root required')
    observed={'version':1,'machine_id':Path('/etc/machine-id').read_text().strip(),
        'boot_id':Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
        'data_uuid':run(['/usr/bin/findmnt','-n','-o','UUID','--target','/data']),
        'native':{},'artifacts':{},'units':{},'metadata_paths':[],'support_mounts':{}}
    need(run(['/usr/bin/findmnt','-n','-o','FSTYPE','--target','/data'])=='xfs','reviewed XFS data mount required')
    for name,relative in NATIVE.items():
        target='cube-sandbox-'+name+'.service';state=unit(target);pid=int(state.get('MainPID','0'))
        need(state.get('ActiveState')=='active' and pid>1,'actual active native MainPID required')
        generation=start_time(pid);exe=Path(f'/proc/{pid}/exe').resolve(strict=True)
        need(exe==TOOLBOX/relative,'native wrapper has not handed off to the exact reviewed executable')
        checksum=digest(exe)
        need(start_time(pid)==generation and unit(target).get('MainPID')==str(pid),'native process changed while inspecting')
        observed['native'][name]={'path':str(exe),'sha256':checksum,'pid':pid,'start_time':generation,'unit_type':state['Type']}
    names=['cube-sandbox-'+name+'.service' for name in SERVICES]+['cube-sandbox-control.target','cube-sandbox-compute.target','docker.service','docker.socket']
    for name in names:
        state=unit(name);observed['units'][name]={'active':state['ActiveState'],'type':state.get('Type')}
        if name=='cube-sandbox-s3lvol.service':need(state['ActiveState']=='inactive' and state.get('MainPID')=='0','unsupported s3lvol must remain inactive')
        for value in [state['FragmentPath'],*state.get('DropInPaths','').split()]:
            path=real(value)
            # Vendor Docker units are recorded in evidence, but existing runtime
            # artifact policy pins their reviewed /etc drop-ins separately.
            checksum=digest(path)
            if path.is_relative_to('/etc/systemd/system'):observed['artifacts'][str(path)]=checksum
            else:observed['units'][name]['vendor_fragment']={'path':str(path),'sha256':checksum}
    paths=[TOOLBOX/'.one-click.env',TOOLBOX/'Cubelet/config/config.toml',TOOLBOX/'CubeMaster/conf.yaml',TOOLBOX/'CubeTemplateCenter/conf.yaml']
    # Hash code/config only, never copy contents (in particular .one-click.env).
    for base in [TOOLBOX/'scripts/systemd',TOOLBOX/'scripts/one-click']:
        paths.extend(sorted(base.glob('*.sh')))
    for base in [TOOLBOX/'support',TOOLBOX/'cubeproxy',TOOLBOX/'cube-lifecycle-manager']:
        if base.exists():paths.extend(sorted(base.glob('*.yaml')));paths.extend(sorted(base.glob('*.yml')))
    need(len(paths)<=512,'artifact list unexpectedly large')
    for path in paths:
        need(path.stat().st_size<=32*1024*1024,'configuration artifact too large')
        observed['artifacts'][str(path)]=digest(path)
    config=tomllib.loads((TOOLBOX/'Cubelet/config/config.toml').read_text())
    need(config.get('durable_metadata_root')==str(META),'durable metadata upgrade not configured')
    for plugin,(key,child) in PLUGINS.items():
        expected=META/child;need(config.get('plugins',{}).get(plugin,{}).get(key)==str(expected),'persistent plugin path mismatch')
        real(expected);need(expected.is_dir() and expected.stat().st_dev==Path('/data').stat().st_dev,'plugin metadata outside data filesystem')
        observed['metadata_paths'].append(str(expected))
    need(config['plugins']['io.containerd.metadata.v1.bolt'].get('no_sync') is False,'containerd metadata fsync disabled')
    for name,destination in STATEFUL.items():
        identity=run(['/usr/bin/docker','inspect','--format','{{.Id}}',name]);need(SHA.fullmatch(identity),'full stateful container identity required')
        mounts=json.loads(run(['/usr/bin/docker','inspect','--format','{{json .Mounts}}',identity]))
        selected=[m for m in mounts if m.get('Destination')==destination]
        need(len(selected)==1 and selected[0].get('Type') in ('bind','volume') and selected[0].get('RW') is True,'stateful support mount not retained')
        path=real(selected[0]['Source']);need(path.is_dir() and path.stat().st_dev in (Path('/').stat().st_dev,Path('/data').stat().st_dev),'support data not on paired persistent disks')
        observed['support_mounts'][name]={'container_id':identity,'source':str(path),'destination':destination,'device':path.stat().st_dev}
    fmt='{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Image}},"restart":{{json .HostConfig.RestartPolicy}},"ports":{{json .HostConfig.PortBindings}},"mounts":{{json .Mounts}}}'
    observed['registry']=json.loads(run(['/usr/bin/docker','inspect','--format',fmt,'cube-production-registry']))
    validate_registry(observed['registry'])
    need(observed['registry']['id']==observed['support_mounts']['cube-production-registry']['container_id'],'registry changed during collection')
    return observed

def validate_registry(value):
    need(isinstance(value,dict) and set(value)=={'id','name','image','restart','ports','mounts'},'exact registry definition required')
    need(SHA.fullmatch(value.get('id','')) and value.get('name')=='/cube-production-registry','registry identity invalid')
    need(re.fullmatch(r'sha256:[a-f0-9]{64}',value.get('image','')),'registry image digest required')
    need(value.get('restart')=={'Name':'always','MaximumRetryCount':0},'registry requires reviewed always restart policy')
    need(value.get('ports')=={'5000/tcp':[{'HostIp':'127.0.0.1','HostPort':'5000'}]},'registry loopback binding differs')
    mounts=value.get('mounts')
    need(isinstance(mounts,list) and len(mounts)==1 and mounts[0].get('Type') in ('bind','volume') and mounts[0].get('Destination')=='/var/lib/registry' and mounts[0].get('RW') is True,'retained registry data mount required')
    source=Path(mounts[0].get('Source',''))
    need(source.is_absolute() and '..' not in source.parts,'registry data source invalid')


def overrides():
    output={}
    for name in SERVICES:
        unit='cube-sandbox-'+name+'.service'
        output['/etc/systemd/system/'+unit+'.d/99-baarcha-retained-stop.conf']='[Service]\nRestart=no\nSendSIGKILL=no\nTimeoutStopSec=infinity\nKillMode=process\nExecStop=\nExecStop=/usr/bin/python3 '+SCRIPT+' stop-component --unit '+unit+'\n'
    output['/etc/systemd/system/docker.service.d/99-baarcha-retained-stop.conf']='[Service]\nRestart=no\nSendSIGKILL=no\nTimeoutStopSec=infinity\nKillMode=process\nKillSignal=SIGTERM\nExecStop=\n'
    return output

def render(observed,helper_sha):
    need(observed.get('version')==1 and SHA.fullmatch(helper_sha),'valid collected metadata and helper digest required')
    need(re.fullmatch(r'[a-f0-9]{32}',observed.get('machine_id','')) and re.fullmatch(r'[a-f0-9-]{32,64}',observed.get('data_uuid','')),'actual worker identities required')
    need(set(observed.get('native',{}))==set(NATIVE),'all native process identities required')
    for name,path in NATIVE.items():
        value=observed['native'][name]
        need(value['path']==str(TOOLBOX/path) and SHA.fullmatch(value['sha256']) and value['pid']>1,'wrong native binary identity')
        need(value['unit_type']==('forking' if name=='cubelet' else 'simple'),'unexpected native service type')
    need(set(observed.get('metadata_paths',[]))=={str(META/child) for _,child in PLUGINS.values()},'all persistent plugin paths required')
    validate_registry(observed.get('registry'))
    staged=overrides();artifacts=dict(observed['artifacts'])
    need(artifacts and all(SHA.fullmatch(v) and (Path(k).is_relative_to('/usr/local/services') or Path(k).is_relative_to('/etc/systemd/system')) for k,v in artifacts.items()),'unexpected artifact scope')
    for name in ['cubeops','cube-templatecenter']:
        entry=observed['native'][name];artifacts[entry['path']]=entry['sha256']
    artifacts[SCRIPT]=helper_sha
    for path,data in staged.items():artifacts[path]=hashlib.sha256(data.encode()).hexdigest()
    manifest={'version':1,'reviewed':False,'machine_id':observed['machine_id'],'data_filesystem_uuid':observed['data_uuid'],
        'binaries':{name:{k:observed['native'][name][k] for k in ('path','sha256')} for name in ['cubelet','cubemaster','cube-api']},
        'artifacts':artifacts,'registry':observed['registry'],'durable_metadata_reviewed':False,'durable_metadata_paths':observed['metadata_paths']}
    return manifest,staged

def write_stage(output,observed,helper):
    parent=real(output.parent);info=parent.stat();need(info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o700,'private root0700 staging parent required')
    need(not output.exists() and not output.is_symlink(),'fresh staging directory required')
    manifest,staged=render(observed,digest(helper));output.mkdir(mode=0o700)
    plan={'version':1,'installed':False,'service_changes':False,'review_required':True,'helper_sha256':digest(helper),'files':[]}
    items={'nested-lifecycle.json':json.dumps(manifest,indent=2)+'\n','observed-private.json':json.dumps(observed,indent=2)+'\n'}
    for index,(destination,data) in enumerate(staged.items()):
        relative='overrides/'+str(index)+'.conf';items[relative]=data
        plan['files'].append({'source':relative,'destination':destination,'sha256':hashlib.sha256(data.encode()).hexdigest(),'mode':'0644'})
    items['install-plan.json']=json.dumps(plan,indent=2)+'\n'
    for relative,data in items.items():
        target=output/relative;target.parent.mkdir(mode=0o700,exist_ok=True)
        fd=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        with os.fdopen(fd,'w') as file:file.write(data);file.flush();os.fsync(file.fileno())
    for directory in [output/'overrides',output,output.parent]:
        fd=os.open(directory,os.O_RDONLY)
        try:os.fsync(fd)
        finally:os.close(fd)
    return {'stage':str(output),'override_count':len(staged),'installed':False,'review_required':True}

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--helper-source',required=True,type=Path);p.add_argument('--output',required=True,type=Path);args=p.parse_args()
    try:print(json.dumps(write_stage(args.output,collect(),args.helper_source)))
    except Exception:print('nested manifest preparation refused; inspect private installed identities',file=sys.stderr);sys.exit(1)
