#!/usr/bin/env python3
"""Journal-replay and export ONLY inside a dedicated networkless rescue VM.

Never run this on the worker or outer host. Input files are independent captured
copies. The exact input ext4 is hashed, then copied again before journal replay.
"""
import argparse
import json
import os
from pathlib import Path
import socket
import re
import stat
import subprocess
from capture import sha256, no_symlink
from plan import Invalid, absolute, relative, require, private_json

def verify_manifest(manifest, source):
    require(manifest.get('format')==1 and manifest.get('purpose')=='CUBE_CURRENT_DISK_RESCUE_INPUT','invalid input manifest')
    upper=relative(manifest['upper_subdir'])
    identity=manifest.get('upper_identity') or {'kind':'direct_container','container_id':manifest['sandbox_id']}
    if identity.get('kind')=='template_snapshot':
        template_id=identity.get('template_id')
        from plan import ID
        require(isinstance(template_id,str) and ID.fullmatch(template_id) is not None,'invalid original template ID')
        require(identity.get('container_id')==template_id+'_0' and identity.get('metadata_path')=='/data/cubelet/storage/xfs/snapshots/'+template_id+'/metadata/metadata.json','original snapshot identity mismatch')
        require(re.fullmatch(r'[a-f0-9]{64}',identity.get('metadata_sha256','')) is not None and identity['metadata_sha256']==manifest.get('source_metadata_sha256',{}).get('template_snapshot_metadata'),'unbound original snapshot metadata')
    else:
        require(identity.get('kind')=='direct_container' and identity.get('container_id')==manifest['sandbox_id'],'direct upper identity mismatch')
    require(upper=='disk/'+identity['container_id']+'/upper','upper identity mismatch')
    files=manifest.get('artifacts',[])
    require(2<=len(files)<=65,'invalid artifact count')
    layout=manifest.get('lower_layout','directory_layers')
    require(layout in ('directory_layers','pmem_ext4'),'unsupported lower layout')
    require(layout!='pmem_ext4' or len(files)==2,'exactly one PMEM lower required')
    expected=['current.ext4']+(['lower-000.ext4'] if layout=='pmem_ext4' else [f'lower-{i:03}.tar' for i in range(len(files)-1)])
    require([f.get('file') for f in files]==expected,'artifact order mismatch')
    total=0
    for f in files:
        p=no_symlink(source/f['file']); st=p.stat()
        require(stat.S_ISREG(st.st_mode) and 0<st.st_size==f['bytes']<=16*1024**3,'invalid artifact size')
        require(sha256(p)==f['sha256'],'artifact digest mismatch')
        total+=st.st_size
    require(total<=32*1024**3,'input bundle exceeds bound')
    return files

def require_rescue():
    require(os.geteuid()==0 and socket.gethostname()=='baarcha-cube-rescue','dedicated rescue VM required')
    virtualization=subprocess.check_output(['systemd-detect-virt','--vm'],text=True).strip()
    require(virtualization in ('kvm','qemu'),'hardware VM required')
    require(all(p.name=='lo' or int((p/'flags').read_text().strip(),16)&1==0 for p in Path('/sys/class/net').iterdir()),'all non-loopback interfaces must be down during rescue')
    mounts=Path('/proc/self/mountinfo').read_text()
    require(not any(' - '+fs+' ' in mounts for fs in ('virtiofs','9p','nfs','nfs4','cifs','fuse')),'no host directory shares allowed')

def run(command, *, acceptable=(0,), **kwargs):
    result=subprocess.run(command,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=600,**kwargs)
    require(result.returncode in acceptable,'rescue command failed; retain clone and report')

def reject_unexportable_metadata(home):
    # Full tree is trusted only as data. Never follow symlinks. The archive
    # validator separately verifies link resolution and ownership before import.
    excluded=[]
    def fail(error): raise error
    entries=0
    inode_links={}
    for directory,dirs,files in os.walk(home,followlinks=False,onerror=fail):
        for name in [*dirs,*files]:
            entries+=1
            require(entries<=500000,'home tree exceeds entry bound')
            p=Path(directory)/name; mode=p.lstat().st_mode
            info=p.lstat()
            if stat.S_ISREG(mode):
                key=(info.st_dev,info.st_ino)
                count,expected=inode_links.get(key,(0,info.st_nlink))
                require(expected==info.st_nlink,'home inode changed during inventory')
                inode_links[key]=(count+1,expected)
            rel=p.relative_to(home).as_posix()
            # The supervisor recreates its Unix control endpoint at startup.
            # Preserve its files/history; only this exact socket inode is ephemeral.
            if stat.S_ISSOCK(mode) and (rel == '.runtimed/sock' or re.fullmatch(r'\.baarcha-postgres/run/\.s\.PGSQL\.[0-9]+',rel)):
                excluded.append(rel)
                continue
            require(stat.S_ISREG(mode) or stat.S_ISDIR(mode) or stat.S_ISLNK(mode),'special file in exported home')
            require(not mode & (stat.S_ISUID|stat.S_ISGID),'privilege bits in exported home')
            require(not os.listxattr(p,follow_symlinks=False),'extended metadata requires an explicit export implementation')

    require(all(count==expected for count,expected in inode_links.values()),'hardlink names extend outside exported home or metadata changed')
    return sorted(excluded)

def export(source, work):
    require_rescue()
    source=no_symlink(absolute(str(source),'/var/lib/cube-rescue'))
    work=Path(absolute(str(work),'/var/lib/cube-rescue'))
    no_symlink(work.parent)
    manifest=json.loads((source/'rescue-input.json').read_text())
    files=verify_manifest(manifest,source)
    require(not work.exists(),'work directory must be new')
    work.mkdir(mode=0o700)
    # Only an independent scratch file is ever passed to fsck, never input/source.
    repaired=work/'journal-replayed.ext4'
    run(['cp','--reflink=auto','--sparse=always','--',str(source/'current.ext4'),str(repaired)])
    os.chmod(repaired,0o600)
    require((repaired.stat().st_dev,repaired.stat().st_ino)!=(source.joinpath('current.ext4').stat().st_dev,source.joinpath('current.ext4').stat().st_ino),'scratch aliases input disk')
    filesystem=subprocess.check_output(['blkid','-p','-o','value','-s','TYPE',str(repaired)],text=True,stderr=subprocess.DEVNULL,timeout=30).strip()
    require(filesystem=='ext4','captured current disk is not ext4')
    run(['e2fsck','-p',str(repaired)],acceptable=(0,1))
    mounted=[]
    try:
        rw=work/'rw'; rw.mkdir(mode=0o700)
        run(['mount','-t','ext4','-o','loop,ro,noload,nosuid,nodev,noexec',str(repaired),str(rw)])
        mounted.append(rw)
        upper=no_symlink(rw/manifest['upper_subdir']); require(upper.is_dir(),'current upper directory missing')
        lowers=[]
        for i,f in enumerate(files[1:]):
            lower=work/f'lower-{i:03}'; lower.mkdir(mode=0o700)
            # These are pinned, trusted OCI lower archives from capture, not
            # customer exports. Overlay whiteouts/xattrs must be retained here.
            if manifest.get('lower_layout')=='pmem_ext4':
                filesystem=subprocess.check_output(['blkid','-p','-o','value','-s','TYPE',str(source/f['file'])],text=True,stderr=subprocess.DEVNULL,timeout=30).strip()
                require(filesystem=='ext4','trusted PMEM lower is not ext4')
                # Immutable image must be clean: never replay or repair its journal.
                run(['e2fsck','-fn',str(source/f['file'])])
                run(['mount','-t','ext4','-o','loop,ro,noload,nosuid,nodev,noexec',str(source/f['file']),str(lower)])
                mounted.append(lower)
            else:
                run(['tar','--numeric-owner','--same-owner','--xattrs','--xattrs-include=*','--acls','-xf',str(source/f['file']),'-C',str(lower)])
            lowers.append(lower)
        merged=work/'merged'; merged.mkdir(mode=0o700)
        options='ro,nosuid,nodev,noexec,lowerdir='+':'.join(str(p) for p in [upper,*lowers])
        run(['mount','-t','overlay','overlay','-o',options,str(merged)])
        mounted.append(merged)
        home=no_symlink(merged/'home/sandbox'); require(home.is_dir(),'home directory missing')
        excluded=reject_unexportable_metadata(home)
        archive=work/'home.tar'
        with archive.open('xb') as out:
            os.chmod(archive,0o600)
            subprocess.run(['tar','--format=pax','--numeric-owner',*['--exclude=./'+p for p in excluded],'-cf','-','-C',str(home),'.'],stdout=out,stderr=subprocess.DEVNULL,check=True,timeout=600)
            out.flush();os.fsync(out.fileno())
        private_json(work/'export-report.json',{'purpose':'CUBE_CURRENT_DISK_HOME_EXPORT','sandbox_id':manifest['sandbox_id'],'archive_bytes':archive.stat().st_size,'archive_sha256':sha256(archive),'captured_disk_sha256':files[0]['sha256'],'excluded_ephemeral_sockets':excluded,'journal_replay':'performed on independent scratch copy only','status':'exported; must pass archive validator before import'})
        require(all(sha256(source/f['file'])==f['sha256'] for f in files),'input artifact changed')
    finally:
        cleanup_failed=False
        for mount in reversed(mounted):
            try: run(['umount',str(mount)])
            except (Invalid,subprocess.SubprocessError): cleanup_failed=True
        require(not cleanup_failed,'mount cleanup incomplete; retain rescue VM for review')
    return archive

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input',required=True,type=Path)
    parser.add_argument('--work',required=True,type=Path)
    args=parser.parse_args()
    try:export(args.input,args.work)
    except (Invalid,OSError,ValueError,TypeError,KeyError,subprocess.SubprocessError):
        parser.exit(1,'Rescue export failed; keep all private inputs and scratch files for review.\n')
if __name__=='__main__':main()
