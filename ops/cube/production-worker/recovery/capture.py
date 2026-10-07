#!/usr/bin/env python3
"""Capture a fenced CURRENT disk without mounting it; intended for worker-local use.

Requires an operator-issued, short-lived fence receipt after a whole-worker boot
or an independently fenced individual runtime loss.
This receipt is an assertion, not an automatic fencing implementation. The operator
must keep controller Create/Connect/Delete and Cube lifecycle mutations disabled.
"""
import argparse
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import socket
import re
import stat
import subprocess
import time
from plan import Invalid, absolute, private_json, require

FICLONE = 0x40049409
IMAGE_ALIAS = Path('/usr/local/services/cubetoolbox/cubebox_os_image')
FLEET_IMAGE_CACHE = Path('/data/cube-fleet-rootfs')

def sha256(path):
    h=hashlib.sha256()
    with open(path,'rb') as f:
        for chunk in iter(lambda:f.read(4*1024*1024),b''): h.update(chunk)
    return h.hexdigest()

def no_symlink(path):
    p=Path(path)
    for component in reversed([p,*p.parents]):
        require(not stat.S_ISLNK(component.lstat().st_mode),'symlink source path rejected')
    return p

def lower_image_source(path):
    """Resolve only the reviewed B200 image-cache alias, never guest disks."""
    source = Path(path)
    if not IMAGE_ALIAS.is_symlink():
        return no_symlink(source)
    require(socket.gethostname() == 'baarcha-cube-worker-b200-01', 'image alias requires reviewed B200 worker')
    no_symlink(IMAGE_ALIAS.parent)
    require(IMAGE_ALIAS.lstat().st_uid == os.geteuid() and
            os.readlink(IMAGE_ALIAS) == str(FLEET_IMAGE_CACHE), 'image cache alias changed')
    relative = source.relative_to(IMAGE_ALIAS)
    target = no_symlink(FLEET_IMAGE_CACHE / relative)
    require(target.stat().st_uid == os.geteuid() and not target.stat().st_mode & 0o022,
            'immutable image must be operator-owned and not publicly writable')
    require((source.stat().st_dev, source.stat().st_ino) == (target.stat().st_dev, target.stat().st_ino),
            'image cache identity changed')
    return target

def validate_fence(fence, plan, boot_id, machine_id, now):
    purpose=fence.get('purpose')
    require(purpose in ('CUBE_CURRENT_DISK_CAPTURE','CUBE_FENCED_RUNTIME_DISK_CAPTURE'),'wrong fence purpose')
    require(fence.get('sandbox_id')==plan['sandbox_id'],'fence sandbox mismatch')
    require(re.fullmatch(r'[a-f0-9]{32}',machine_id) is not None and machine_id!='0'*32 and fence.get('worker_machine_id')==machine_id,'worker identity mismatch')
    require(fence.get('current_boot_id')==boot_id,'worker boot changed')
    if purpose=='CUBE_CURRENT_DISK_CAPTURE':
        require(fence.get('previous_boot_id') not in (None,'',boot_id),'whole-worker loss not independently identified')
    else:
        require(fence.get('provider_requests_drained') is True and fence.get('no_disk_handles_verified') is True,
                'individual runtime requires drained requests and no disk holders')
        identity=fence.get('source_identity')
        require(isinstance(identity,dict) and set(identity)=={'device','inode','bytes','mtime_ns'} and
                all(type(value) is int and value>0 for value in identity.values()),'exact fenced source identity required')
    expiry=fence.get('expires_at',0)
    require(isinstance(expiry,int) and now < expiry <= now+1800,'stale or excessive fence lifetime')
    require(fence.get('no_task_verified') is True and fence.get('management_fenced') is True,'operator must fence all lifecycle mutation and verify no task')

def reflink(source,destination):
    fd=os.open(source,os.O_RDONLY|os.O_NOFOLLOW)
    try:
        before=os.fstat(fd)
        require(stat.S_ISREG(before.st_mode) and before.st_size>0,'current disk must be a nonempty regular backing file')
        out=os.open(destination,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        try:
            fcntl.ioctl(out,FICLONE,fd)  # fail instead of doing a potentially inconsistent streaming copy
            os.fsync(out)
        finally: os.close(out)
        after=os.fstat(fd)
        require((before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns)==(after.st_dev,after.st_ino,after.st_size,after.st_mtime_ns),'current disk changed during capture')
        return {'device':before.st_dev,'inode':before.st_ino,'bytes':before.st_size,'mtime_ns':before.st_mtime_ns}
    finally: os.close(fd)

def copy_immutable(source,destination):
    # Trusted image input only. Current writable disks always require FICLONE.
    fd=os.open(source,os.O_RDONLY|os.O_NOFOLLOW)
    try:
        before=os.fstat(fd)
        require(stat.S_ISREG(before.st_mode) and 0<before.st_size<=16*1024**3,'invalid trusted lower image')
        out=os.open(destination,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        cloned=False
        try:
            # Reviewed image caches and capture stages share XFS on B200.
            # Reflink avoids rewriting gigabytes while management is fenced;
            # all source/destination identity and digest checks still run.
            fcntl.ioctl(out,FICLONE,fd)
            cloned=True
        except OSError as error:
            if error.errno not in (errno.EXDEV,errno.EOPNOTSUPP,errno.ENOTTY,errno.EINVAL):
                os.close(out)
                raise
        first=hashlib.sha256()
        with os.fdopen(out,'wb') as stream:
            while True:
                chunk=os.read(fd,4*1024*1024)
                if not chunk:break
                first.update(chunk)
                if not cloned:stream.write(chunk)
            stream.flush();os.fsync(stream.fileno())
        os.lseek(fd,0,os.SEEK_SET)
        second=hashlib.sha256()
        while True:
            chunk=os.read(fd,4*1024*1024)
            if not chunk:break
            second.update(chunk)
        after=os.fstat(fd)
        identity=lambda st:(st.st_dev,st.st_ino,st.st_size,st.st_mtime_ns,st.st_ctime_ns)
        require(identity(before)==identity(after) and first.digest()==second.digest(),'trusted lower image changed during capture')
        require(sha256(destination)==first.hexdigest(),'trusted lower copy digest mismatch')
        return {'device':before.st_dev,'inode':before.st_ino,'bytes':before.st_size,'mtime_ns':before.st_mtime_ns,'sha256':first.hexdigest()}
    finally:os.close(fd)

def capture(plan,fence,output):
    require(os.geteuid()==0 and socket.gethostname() in ('baarcha-cube-worker-01','baarcha-cube-worker-b200-01'),'reviewed worker root only')
    require(plan.get('purpose')=='CUBE_CURRENT_DISK_RESCUE' and plan.get('format')==1,'invalid plan')
    validate_fence(fence,plan,Path('/proc/sys/kernel/random/boot_id').read_text().strip(),Path('/etc/machine-id').read_text().strip(),int(time.time()))
    root=no_symlink('/data')
    require(os.statvfs(root).f_bavail*os.statvfs(root).f_frsize>=80*1024**3,'less than 80 GiB data headroom')
    output=Path(absolute(str(output),'/data/cube-recovery'))
    no_symlink(output.parent)
    output.mkdir(mode=0o700)
    source=no_symlink(absolute(plan['current_disk']['FilePath']))
    if fence['purpose']=='CUBE_FENCED_RUNTIME_DISK_CAPTURE':
        current=source.stat()
        require(fence['source_identity']=={'device':current.st_dev,'inode':current.st_ino,'bytes':current.st_size,'mtime_ns':current.st_mtime_ns},
                'source changed since individual runtime fence')
    identity=reflink(source,output/'current.ext4')
    artifacts=[{'file':'current.ext4','bytes':(output/'current.ext4').stat().st_size,'sha256':sha256(output/'current.ext4')}]
    for index,path in enumerate(plan['lower_dirs']):
        layer=no_symlink(absolute(path)); require(layer.is_dir(),'lower image layer missing')
        name=f'lower-{index:03}.tar'
        # Image lowerdirs are trusted host-owned OCI material, not the guest upper.
        # Preserve overlay whiteout devices/xattrs here; parsing happens only in rescue VM.
        with (output/name).open('xb') as stream:
            os.chmod(output/name,0o600)
            subprocess.run(['tar','--format=pax','--numeric-owner','--xattrs','--xattrs-include=*','--acls','-cf','-','-C',str(layer),'.'],stdout=stream,stderr=subprocess.DEVNULL,check=True,timeout=600)
            stream.flush(); os.fsync(stream.fileno())
        artifacts.append({'file':name,'bytes':(output/name).stat().st_size,'sha256':sha256(output/name)})
    layout=plan.get('lower_layout','directory_layers')
    require(layout in ('directory_layers','pmem_ext4'),'unsupported lower layout')
    if layout=='pmem_ext4':
        require(not plan['lower_dirs'],'mixed lower layout')
        image=plan['lower_image'];image_id=image['image_id']
        from plan import ID
        require(ID.fullmatch(image_id) is not None and image['filesystem']=='ext4','invalid trusted lower image identity')
        expected='/usr/local/services/cubetoolbox/cubebox_os_image/'+image_id+'/'+image_id+'.ext4'
        require(image['file']==expected,'trusted lower image path mismatch')
        source_image=lower_image_source(expected)
        lower_identity=copy_immutable(source_image,output/'lower-000.ext4')
        require(lower_image_source(expected)==source_image,'image cache mapping changed during capture')
        after_image=source_image.stat()
        require((after_image.st_dev,after_image.st_ino,after_image.st_size,after_image.st_mtime_ns)==
                (lower_identity['device'],lower_identity['inode'],lower_identity['bytes'],lower_identity['mtime_ns']),
                'image source path changed during capture')
        artifacts.append({'file':'lower-000.ext4','bytes':lower_identity['bytes'],'sha256':lower_identity['sha256'],'source_identity':lower_identity,'image_id':image_id})
    current=source.stat()
    require((current.st_dev,current.st_ino,current.st_size,current.st_mtime_ns)==(identity['device'],identity['inode'],identity['bytes'],identity['mtime_ns']),'source identity changed while capturing lower layers')
    private_json(output/'rescue-input.json',{'format':1,'purpose':'CUBE_CURRENT_DISK_RESCUE_INPUT','sandbox_id':plan['sandbox_id'],'upper_subdir':plan['upper_subdir'],'upper_identity':plan.get('upper_identity'),'source_identity':identity,'source_metadata_sha256':plan['source_metadata_sha256'],'lower_layout':layout,'artifacts':artifacts})
    fd=os.open(output,os.O_RDONLY|os.O_DIRECTORY)
    try: os.fsync(fd)
    finally: os.close(fd)

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    for field in ('plan','fence','output'):parser.add_argument('--'+field,required=True,type=Path)
    args=parser.parse_args()
    try:
        with open('/run/lock/cube-operator-acceptance.lock','a') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            capture(json.loads(args.plan.read_text()),json.loads(args.fence.read_text()),args.output)
    except (Invalid,OSError,ValueError,TypeError,KeyError,subprocess.SubprocessError):
        parser.exit(1,'Capture failed; source is not modified, retain partial private stage for review.\n')

if __name__=='__main__':main()
