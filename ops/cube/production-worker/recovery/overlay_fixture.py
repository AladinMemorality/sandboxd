#!/usr/bin/env python3
"""PREPARED native fixture: execute only in the explicitly handed-off rescue VM.

Creates one synthetic128MiB ext4 file and trusted tiny lower layers. No physical
host/customer disk is opened; no VM power or network-interface operation exists.
"""
import argparse
import errno
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import time
import uuid

from archive_validate import validate
from archive_convert import convert
from capture import sha256, no_symlink
from plan import require, absolute
from rescue_export import export, require_rescue

SIZE = 128 << 20


def run(command, timeout=60):
    subprocess.run(command, check=True, timeout=timeout, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def private_json(path, value):
    with path.open('x') as stream:
        os.chmod(path, 0o600)
        json.dump(value, stream, indent=2)
        stream.flush(); os.fsync(stream.fileno())


def owner_tree(root):
    for directory, dirs, files in os.walk(root, followlinks=False):
        for target in [Path(directory), *(Path(directory)/name for name in dirs+files)]:
            info = target.lstat()
            if (info.st_uid, info.st_gid) != (1000, 1000): os.lchown(target, 1000, 1000)


def write(path, data, mode=0o600):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('wb') as stream:
        os.chmod(path, mode); stream.write(data); stream.flush(); os.fsync(stream.fileno())
    directory = os.open(path.parent, os.O_DIRECTORY | os.O_RDONLY)
    try: os.fsync(directory)
    finally: os.close(directory)


def seed_lower(root, marker):
    home = root/'home/sandbox'
    for directory in ['workspace/app', '.cache/opaque', '.baarcha-postgres/data/global',
                      '.baarcha-postgres/data/pg_wal', '.cube-crash-fixture', '.runtimed/tasks']:
        (home/directory).mkdir(parents=True, exist_ok=True)
    for name in ['.bashrc', '.bash_logout', '.profile']: write(home/name, b'# synthetic\n')
    write(home/'workspace/app/overwrite.txt', b'older')
    write(home/'workspace/app/deleted.txt', b'must disappear')
    write(home/'workspace/app/removed-dir/hidden.txt', b'must disappear')
    write(home/'.cache/opaque/lower-hidden.txt', b'must remain hidden by trusted opaque layer')
    write(home/'.baarcha-postgres/data/PG_VERSION', b'18\n')
    write(home/'.baarcha-postgres/data/global/pg_control', bytes(8192))
    write(home/'.baarcha-postgres/data/pg_wal' / ('A'*24), b'SYNTHETIC WAL FORMAT NOT A DATABASE\n')
    write(home/'.baarcha-postgres/data/postmaster.pid', b'123\n')
    write(home/'.runtimed/tasks/owned-artifact.txt', b'separate task history, not converted')
    for name in ['workspace/app/crash-fixture-data/marker.json', '.cube-crash-fixture/marker.json']:
        write(home/name, json.dumps(marker).encode())
    owner_tree(home)
    write(root/'root/not-exported.txt', b'owned fixture outside home, never export')


def expected_semantics(index, stream):
    for absent in ['workspace/app/deleted.txt', 'workspace/app/removed-dir/hidden.txt',
                   '.cache/opaque/lower-hidden.txt', 'root/not-exported.txt']:
        require(absent not in index, 'overlay deletion or opaque lower semantics lost')
    expected = {'workspace/app/overwrite.txt': b'latest',
                'workspace/app/removed-dir/new.txt': b'visible recreated opaque directory',
                '.cache/opaque/higher.txt': b'higher opaque layer retained',
                'workspace/app/target.txt': b'hardlinked data'}
    for name, data in expected.items():
        item = index[name]
        for _ in range(129):
            if item['kind'] != 'hardlink': break
            item = index[Path(item['target']).as_posix()]
        require(item['kind']=='file', 'expected regular merged file')
        stream.seek(item['offset']); require(stream.read(item['size'])==data, 'latest merged content differs')
    require(index['workspace/app/target.txt']['mode']==0o640, 'mode lost')
    require(index['workspace/app/symbolic']['kind']=='symlink' and index['workspace/app/symbolic']['target']=='target.txt', 'symlink lost')
    pair = [index['workspace/app/target.txt']['kind'], index['workspace/app/hard.txt']['kind']]
    # GNU tar picks one physical name as regular and the other as hardlink.
    require(sorted(pair)==['file','hardlink'], 'hardlink relation was not exported')


def fixture(stage, checker, checker_hash):
    require_rescue()
    stage = Path(absolute(str(stage), '/var/lib/cube-rescue'))
    require(stage.parent == Path('/var/lib/cube-rescue') and stage.name.startswith('overlay-fixture-'), 'synthetic rescue stage required')
    no_symlink(stage)
    info=stage.stat(); require(info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o700, 'root private fixture stage required')
    handoff=stage/'handoff.json'; info=handoff.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o600 and info.st_size<4096, 'root private handoff required')
    scope=json.loads(handoff.read_text())
    require(scope.get('purpose')=='SYNTHETIC_OVERLAY_RESCUE_ONLY' and scope.get('image_bytes')==SIZE and time.time()<scope.get('expires_at',0)<=time.time()+1800, 'fresh exact fixture handoff required')
    require(sha256(no_symlink(checker))==checker_hash, 'reviewed checker hash mismatch')
    require(not (stage/'result.json').exists() and not (stage/'input').exists(), 'do not overwrite prior fixture')
    marker={'fixture':uuid.uuid4().hex,'phase':'baseline','nonce':uuid.uuid4().hex}
    latest={**marker,'phase':'latest','nonce':uuid.uuid4().hex}
    private_json(stage/'latest.json',latest)
    lower=stage/'lower-base'; higher=stage/'lower-higher'; lower.mkdir(); higher.mkdir()
    seed_lower(lower,marker)
    opaque=higher/'home/sandbox/.cache/opaque'; opaque.mkdir(parents=True)
    write(opaque/'higher.txt',b'higher opaque layer retained')
    owner_tree(higher/'home/sandbox')
    os.setxattr(opaque,b'trusted.overlay.opaque',b'y')
    input_dir=stage/'input'; input_dir.mkdir(mode=0o700)
    disk=input_dir/'current.ext4'
    with disk.open('xb') as stream: os.chmod(disk,0o600);stream.truncate(SIZE)
    run(['mkfs.ext4','-q','-F',str(disk)])
    mounted=[]
    rw=stage/'rw'; merged=stage/'merged'; rw.mkdir();merged.mkdir()
    sandbox_id=uuid.uuid4().hex
    def mount_live():
        run(['mount','-t','ext4','-o','loop,nosuid,nodev,noexec',str(disk),str(rw)]);mounted.append(rw)
        upper=rw/'disk'/sandbox_id/'upper'; work=rw/'disk'/sandbox_id/'work'
        upper.mkdir(parents=True,exist_ok=True);work.mkdir(exist_ok=True)
        opts=f'lowerdir={higher}:{lower},upperdir={upper},workdir={work},nosuid,nodev,noexec'
        run(['mount','-t','overlay','overlay','-o',opts,str(merged)]);mounted.append(merged)
    def unmount_all():
        while mounted:
            target=mounted[-1];run(['umount',str(target)]);mounted.pop()
    try:
        mount_live()
        home=merged/'home/sandbox'
        # Copy the baseline into current upper, then take an independent clean
        # older disk backup before any latest marker or deletion operation.
        for name in ['workspace/app/crash-fixture-data/marker.json','.cube-crash-fixture/marker.json']:
            write(home/name,json.dumps(marker).encode())
        owner_tree(home);run(['sync']);unmount_all()
        older=stage/'independent-older.ext4'
        run(['cp','--reflink=never','--sparse=always','--',str(disk),str(older)])
        os.chmod(older,0o600); older_hash=sha256(older)
        mount_live();home=merged/'home/sandbox'
        write(home/'workspace/app/overwrite.txt',b'latest')
        (home/'workspace/app/deleted.txt').unlink()
        shutil.rmtree(home/'workspace/app/removed-dir')
        write(home/'workspace/app/removed-dir/new.txt',b'visible recreated opaque directory')
        write(home/'workspace/app/target.txt',b'hardlinked data',0o640)
        os.link(home/'workspace/app/target.txt',home/'workspace/app/hard.txt')
        os.symlink('target.txt',home/'workspace/app/symbolic')
        for name in ['workspace/app/crash-fixture-data/marker.json','.cube-crash-fixture/marker.json']:
            write(home/name,json.dumps(latest).encode())
        owner_tree(home);run(['sync']);unmount_all()
        for i,layer in enumerate([higher,lower]):
            run(['tar','--format=pax','--numeric-owner','--xattrs','--xattrs-include=*','--acls','-cf',str(input_dir/f'lower-{i:03}.tar'),'-C',str(layer),'.'])
        # Negative control: --xattrs alone excludes trusted.* on extraction.
        bad=stage/'negative-default-xattrs';bad.mkdir()
        run(['tar','--xattrs','-xf',str(input_dir/'lower-000.tar'),'-C',str(bad)])
        omitted=False
        try: os.getxattr(bad/'home/sandbox/.cache/opaque',b'trusted.overlay.opaque')
        except OSError as error:
            if error.errno == errno.ENODATA: omitted=True
            else: raise
        require(omitted,'negative control no longer reproduces default trusted-xattr omission')
        files=['current.ext4','lower-000.tar','lower-001.tar']
        private_json(input_dir/'rescue-input.json',dict(format=1,purpose='CUBE_CURRENT_DISK_RESCUE_INPUT',sandbox_id=sandbox_id,upper_subdir=f'disk/{sandbox_id}/upper',artifacts=[dict(file=name,bytes=(input_dir/name).stat().st_size,sha256=sha256(input_dir/name)) for name in files]))
        archive=export(input_dir,stage/'actual-export')
        with archive.open('rb') as stream:
            checked,index=validate(stream,require_postgres=True,expected_marker=latest,return_index=True)
            expected_semantics(index,stream)
        conversion=convert(archive,stage/'converted',latest,go_validator=checker)
        require(conversion['go_import_contract_validated'],'Go private app/home-v2 contracts rejected')
        require(sha256(older)==older_hash,'independent earlier backup was modified')
        # Inspect old upper directly, read-only: these files were explicitly
        # copied up before the independent older backup was taken.
        oldmount=stage/'old-readonly';oldmount.mkdir()
        run(['mount','-t','ext4','-o','loop,ro,noload,nosuid,nodev,noexec',str(older),str(oldmount)]);mounted.append(oldmount)
        oldhome=oldmount/f'disk/{sandbox_id}/upper/home/sandbox'
        for name in ['workspace/app/crash-fixture-data/marker.json','.cube-crash-fixture/marker.json']:
            require(json.loads((oldhome/name).read_text())==marker,'older backup is not distinct baseline')
        unmount_all()
        result=dict(passed=True,native_overlay=True,image_bytes=SIZE,whiteout_delete=True,opaque_lower_xattr=True,
                    negative_default_xattrs_omitted_trusted=True,overwrite=True,recreated_opaque_directory=True,
                    symlinks_hardlinks_modes=True,older_backup_distinct_unchanged=True,
                    actual_rescue_export=True,archive_validation=checked,conversion=conversion,
                    postgres_crash_recovery_proven=False,power_loss_performed=False,production_accepted=False)
        private_json(stage/'result.json',result)
        return result
    finally:
        unmount_all()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage',type=Path,required=True)
    parser.add_argument('--go-validator',type=Path,required=True)
    parser.add_argument('--checker-sha256',required=True)
    args=parser.parse_args()
    fixture(args.stage,args.go_validator,args.checker_sha256)

if __name__=='__main__':main()
