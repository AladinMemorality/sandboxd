#!/usr/bin/env python3
"""Read-only inventory for exact control-file ownership hardening. Never applies it."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import subprocess
import sys

BASE=Path('/usr/local/services/cubetoolbox')
NATIVE=('Cubelet/bin/cubelet','CubeMaster/bin/cubemaster','CubeAPI/bin/cube-api','CubeOps/bin/cubeops','CubeTemplateCenter/bin/templatecenter')
FIXED=(*NATIVE,'.one-click.env','Cubelet/config/config.toml','Cubelet/dynamicconf/conf.yaml','CubeMaster/conf.yaml','CubeTemplateCenter/conf.yaml')
SERVICES=('mysql','redis','minio','coredns','dns','cubeops','cubemaster','cube-api','cubelet','cube-templatecenter','cube-lifecycle-manager','cube-proxy','cube-egress-net','cube-egress','webui','s3lvol')

def need(value,message):
    if not value:raise RuntimeError(message)

def in_scope(path):
    return path==Path('/usr/local/services') or path==BASE or path.is_relative_to(BASE) or path==Path('/etc/systemd/system') or path.is_relative_to('/etc/systemd/system')

def entry(path):
    need(in_scope(path) and path.is_absolute() and path.resolve(strict=True)==path,'canonical control path required')
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
    try:
        before=os.fstat(fd)
        need(not before.st_mode&0o022,'group/world writable control input requires separate mode review')
        kind='directory' if stat.S_ISDIR(before.st_mode) else 'file'
        need(kind=='directory' or (stat.S_ISREG(before.st_mode) and before.st_nlink==1),'special or multiply linked control input refused')
        checksum=None
        if kind=='file':
            h=hashlib.sha256()
            while True:
                chunk=os.read(fd,1024*1024)
                if not chunk:break
                h.update(chunk)
            checksum=h.hexdigest()
        after=os.fstat(fd)
        need((before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns,before.st_ctime_ns)==(after.st_dev,after.st_ino,after.st_size,after.st_mtime_ns,after.st_ctime_ns),'control input changed during hash')
        return {'path':str(path),'kind':kind,'device':before.st_dev,'inode':before.st_ino,'size':before.st_size,'mode':oct(stat.S_IMODE(before.st_mode)),
            'uid':before.st_uid,'gid':before.st_gid,'sha256':checksum,'desired_uid':0,'desired_gid':0,'ownership_change_required':before.st_uid!=0 or before.st_gid!=0}
    finally:os.close(fd)

def collect():
    need(sys.platform=='linux' and os.geteuid()==0 and socket.gethostname()=='baarcha-cube-worker-01','nested Linux root required')
    paths={BASE/relative for relative in FIXED}
    for relative in ('scripts/systemd','scripts/one-click'):
        paths.update((BASE/relative).glob('*.sh'))
    for relative in ('support','cubeproxy','cube-lifecycle-manager'):
        for suffix in ('*.yaml','*.yml'):paths.update((BASE/relative).glob(suffix))
    units=['cube-sandbox-'+name+'.service' for name in SERVICES]+['cube-sandbox-control.target','cube-sandbox-compute.target','docker.service','docker.socket']
    for unit in units:
        raw=subprocess.check_output(['/usr/bin/systemctl','show',unit,'-p','LoadState','-p','FragmentPath','-p','DropInPaths'],timeout=10,stderr=subprocess.DEVNULL).decode()
        values=dict(line.split('=',1) for line in raw.splitlines() if '=' in line)
        need(values.get('LoadState')=='loaded','expected loaded unit absent')
        for value in [values['FragmentPath'],*values.get('DropInPaths','').split()]:
            path=Path(value)
            if path.is_relative_to('/etc/systemd/system'):paths.add(path)
            else:
                info=path.stat();need(info.st_uid==0 and not info.st_mode&0o022,'vendor unit requires separate review')
    need(len(paths)<=512,'unexpected control inventory size')
    # Parent ownership matters: a non-root owner can replace root-owned files.
    for path in list(paths):
        for parent in path.parents:
            if not in_scope(parent):break
            paths.add(parent)
    rows=[entry(path) for path in sorted(paths,key=lambda p:(len(p.parts),str(p)))]
    return {'version':1,'applied':False,'worker_machine_id':Path('/etc/machine-id').read_text().strip(),'entries':rows}

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--output',required=True,type=Path);args=parser.parse_args()
    try:
        parent=args.output.parent;need(parent.resolve(strict=True)==parent and parent.stat().st_uid==0 and stat.S_IMODE(parent.stat().st_mode)==0o700,'private root0700 output parent required')
        result=collect();fd=os.open(args.output,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        with os.fdopen(fd,'w') as stream:json.dump(result,stream,indent=2);stream.write('\n');stream.flush();os.fsync(stream.fileno())
        fd=os.open(parent,os.O_RDONLY)
        try:os.fsync(fd)
        finally:os.close(fd)
        print(json.dumps({'entries':len(result['entries']),'ownership_changes':sum(row['ownership_change_required'] for row in result['entries']),'applied':False}))
    except Exception:print('ownership inventory refused; review private control inputs',file=sys.stderr);sys.exit(1)
