#!/usr/bin/env python3
"""Rescue-only native proof for the observed pinned PMEM ext4 lower layout.

Two newly created 128MiB synthetic files only; requires explicit fresh handoff.
No guest/host network, real storage input, or worker mutation.
"""
import argparse
import json
import os
from pathlib import Path
import stat
import time
import uuid
import shutil
from overlay_fixture import SIZE, run, write, seed_lower, owner_tree, private_json, expected_semantics
from capture import sha256, no_symlink
from plan import require, absolute
from rescue_export import export, require_rescue
from archive_validate import validate
from archive_convert import convert


def fixture(stage, checker, checker_hash):
    require_rescue()
    stage=Path(absolute(str(stage),'/var/lib/cube-rescue'))
    require(stage.parent==Path('/var/lib/cube-rescue') and stage.name.startswith('pmem-fixture-'),'synthetic PMEM stage required')
    no_symlink(stage)
    info=stage.stat();require(info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o700,'private root stage required')
    handoff=stage/'handoff.json';info=handoff.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o600 and info.st_size<4096,'private handoff required')
    scope=json.loads(handoff.read_text())
    require(scope.get('purpose')=='SYNTHETIC_PMEM_RESCUE_ONLY' and scope.get('image_bytes')==SIZE and time.time()<scope.get('expires_at',0)<=time.time()+1800,'fresh exact handoff required')
    require(sha256(no_symlink(checker))==checker_hash,'checker identity mismatch')
    input_dir=stage/'input';input_dir.mkdir(mode=0o700)
    marker={'fixture':uuid.uuid4().hex,'phase':'baseline','nonce':uuid.uuid4().hex}
    latest={**marker,'phase':'latest','nonce':uuid.uuid4().hex}
    private_json(stage/'latest.json',latest)
    sandbox_id=uuid.uuid4().hex
    disk=input_dir/'current.ext4';lower_disk=input_dir/'lower-000.ext4'
    for image in (disk,lower_disk):
        with image.open('xb') as stream:os.chmod(image,0o600);stream.truncate(SIZE)
        run(['mkfs.ext4','-q','-F',str(image)])
    lower=stage/'lower';rw=stage/'rw';merged=stage/'merged'
    for path in (lower,rw,merged):path.mkdir(mode=0o700)
    mounted=[]
    def mount_image(image,target,readonly=False):
        opts='loop,nosuid,nodev,noexec'+(',ro,noload' if readonly else '')
        run(['mount','-t','ext4','-o',opts,str(image),str(target)]);mounted.append(target)
    def unmount_all():
        while mounted:
            run(['umount',str(mounted[-1])]);mounted.pop()
    def mount_live():
        mount_image(lower_disk,lower,True);mount_image(disk,rw)
        upper=rw/f'disk/{sandbox_id}/upper';work=rw/f'disk/{sandbox_id}/work'
        upper.mkdir(parents=True,exist_ok=True);work.mkdir(exist_ok=True)
        run(['mount','-t','overlay','overlay','-o',f'lowerdir={lower},upperdir={upper},workdir={work},nosuid,nodev,noexec',str(merged)]);mounted.append(merged)
    try:
        mount_image(lower_disk,lower)
        seed_lower(lower,marker)
        # This single root image already contains its complete lower root tree.
        hidden=lower/'home/sandbox/.cache/opaque/lower-hidden.txt';hidden.unlink()
        write(hidden.parent/'higher.txt',b'higher opaque layer retained')
        owner_tree(lower/'home/sandbox');run(['sync']);unmount_all()
        lower_hash=sha256(lower_disk)
        mount_live();home=merged/'home/sandbox'
        for name in ('workspace/app/crash-fixture-data/marker.json','.cube-crash-fixture/marker.json'):write(home/name,json.dumps(marker).encode())
        owner_tree(home);run(['sync']);unmount_all()
        older=stage/'independent-older.ext4'
        run(['cp','--reflink=never','--sparse=always','--',str(disk),str(older)]);os.chmod(older,0o600)
        older_hash=sha256(older)
        mount_live();home=merged/'home/sandbox'
        write(home/'workspace/app/overwrite.txt',b'latest')
        (home/'workspace/app/deleted.txt').unlink()
        shutil.rmtree(home/'workspace/app/removed-dir')
        write(home/'workspace/app/removed-dir/new.txt',b'visible recreated opaque directory')
        write(home/'workspace/app/target.txt',b'hardlinked data',0o640)
        os.link(home/'workspace/app/target.txt',home/'workspace/app/hard.txt')
        os.symlink('target.txt',home/'workspace/app/symbolic')
        for name in ('workspace/app/crash-fixture-data/marker.json','.cube-crash-fixture/marker.json'):write(home/name,json.dumps(latest).encode())
        owner_tree(home);run(['sync']);unmount_all()
        require(sha256(lower_disk)==lower_hash,'read-only lower was modified')
        names=['current.ext4','lower-000.ext4']
        private_json(input_dir/'rescue-input.json',dict(format=1,purpose='CUBE_CURRENT_DISK_RESCUE_INPUT',sandbox_id=sandbox_id,upper_subdir=f'disk/{sandbox_id}/upper',lower_layout='pmem_ext4',artifacts=[dict(file=n,bytes=(input_dir/n).stat().st_size,sha256=sha256(input_dir/n)) for n in names]))
        archive=export(input_dir,stage/'actual-export')
        with archive.open('rb') as stream:
            checked,index=validate(stream,require_postgres=True,expected_marker=latest,return_index=True)
            expected_semantics(index,stream)
        conversion=convert(archive,stage/'converted',latest,go_validator=checker)
        require(conversion['go_import_contract_validated'],'Go import contract failed')
        require(sha256(older)==older_hash and sha256(lower_disk)==lower_hash,'independent input mutated')
        mount_image(older,rw,True)
        for name in ('workspace/app/crash-fixture-data/marker.json','.cube-crash-fixture/marker.json'):
            require(json.loads((rw/f'disk/{sandbox_id}/upper/home/sandbox'/name).read_text())==marker,'old backup did not retain distinct baseline')
        unmount_all()
        result=dict(passed=True,lower_layout='pmem_ext4',native_overlay=True,whiteout_delete=True,recreated_opaque_directory=True,symlinks_hardlinks_modes=True,latest_markers=True,older_backup_distinct_unchanged=True,lower_image_unchanged=True,actual_rescue_export=True,conversion=conversion,archive_validation=checked,postgres_crash_recovery_proven=False,power_loss_performed=False,production_accepted=False)
        private_json(stage/'result.json',result)
        return result
    finally:unmount_all()

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage',type=Path,required=True)
    parser.add_argument('--go-validator',type=Path,required=True)
    parser.add_argument('--checker-sha256',required=True)
    args=parser.parse_args();fixture(args.stage,args.go_validator,args.checker_sha256)
