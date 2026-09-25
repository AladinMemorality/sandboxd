#!/usr/bin/env python3
"""Read-only enrollment preflight/readiness. Never install, start, or fix services."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tomllib
from render_config import PLUGINS, ROOT, render

CONFIG=Path('/usr/local/services/cubetoolbox/Cubelet/config/config.toml')
BINARY=Path('/usr/local/services/cubetoolbox/Cubelet/bin/cubelet')
CANDIDATE=Path('/root/cube-production/containerd-durable-native-candidate/cubelet-candidate')
CANDIDATE_SHA='de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b'
BASELINE_SHA='254b5e7b11c7c6f865e3e1816c3737a4041424b7e84b5aba7dc406e596eecbed'
DATA_UUID='793c3349-db9c-4815-9842-989ed484f1f8'
CRITICAL_UNITS=['cubelet','cubemaster','cube-api','cube-proxy','cube-templatecenter','cube-lifecycle-manager','cubeops','webui']
RETAINED=Path('/data/cubelet/storage/xfs/objects/volumes/tpl-tpl-ce1ee426e686460bbc8c3bfc-build-rootfs/sb-1a1474444e064d6f8da340f324f4fd7f-rootfs-gen0')

def need(condition, message):
    if not condition: raise ValueError(message)

def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.PIPE, timeout=30).strip()

def sha(path):
    with Path(path).open('rb') as f: return hashlib.file_digest(f,'sha256').hexdigest()

def no_symlinks(path):
    path=Path(path)
    for parent in [path,*path.parents]: need(not parent.is_symlink(),'unexpected symlink in reviewed path')

def check_config(current, old):
    need(tomllib.loads(current)==tomllib.loads(render(old)), 'config changed beyond reviewed durable metadata fields (including startup rewrite)')

def check_open_roots(paths):
    result={sub:[] for _,sub in PLUGINS.values()}
    for raw in paths:
        need(not (raw.startswith(ROOT+'/') and raw.endswith(' (deleted)')), 'deleted persistent DB remains open')
        if raw.startswith('/data/cubelet/state/') and raw.endswith('.db'):
            need(raw=='/data/cubelet/state/io.containerd.mount-manager.v1.bolt/mounts.db','critical database still open under volatile State')
        for sub in result:
            if raw.startswith(ROOT+'/'+sub+'/') and raw.endswith('.db'): result[sub].append(raw)
    for sub,paths in result.items():
        # Netfile stores per-guest files, not a Bolt DB, and is lazy on empty nodes.
        if sub!='netfile': need(paths, 'no actual open database under reviewed root: '+sub)
    return {sub:len(paths) for sub,paths in result.items()}

def check_template(item, expected):
    need(item.get('status')=='READY','required template not ready')
    request=item['create_request'];containers=request['containers']
    need(len(containers)==1,'template must have one container')
    container=containers[0]
    need(container['resources']=={'cpu':'2000m','mem':'2048Mi'},'template resource mismatch')
    need(request['cube_network_config']=={'denyOut':['0.0.0.0/0']},'template network mismatch')
    need(any(v.get('volume_source',{}).get('empty_dir',{}).get('size_limit')=='10Gi' for v in request['volumes']),'template writable layer mismatch')
    probe=container['probe']['probe_handler']['http_get']
    need(probe['port']==49983 and probe['path']=='/health','template probe mismatch')
    # Resource request is checked by exact immutable requested template ID; do not
    # infer a changed ID from alias or silently recreate missing templates.
    need(re.fullmatch(r'tpl-[0-9a-f]{24}',expected['template_id']) is not None,'invalid expected template identity')

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('phase',choices=['before-install','ready'])
    p.add_argument('--old-config',type=Path,required=True)
    p.add_argument('--machine-id',required=True)
    p.add_argument('--previous-boot-id',required=True)
    p.add_argument('--templates',type=Path)
    a=p.parse_args()
    need(os.geteuid()==0 and run('hostname')=='baarcha-cube-worker-01','verified root worker required')
    need(re.fullmatch('[0-9a-f]{32}',a.machine_id) is not None and a.machine_id!='0'*32,'invalid machine identity')
    need(Path('/etc/machine-id').read_text().strip()==a.machine_id,'worker machine identity mismatch')
    boot=Path('/proc/sys/kernel/random/boot_id').read_text().strip()
    need(re.fullmatch('[0-9a-f-]{36}',a.previous_boot_id) is not None and boot!=a.previous_boot_id,'clean reboot not established')
    need(run('findmnt','-n','-o','UUID','-T','/data')==DATA_UUID,'data UUID mismatch')
    need(run('findmnt','-n','-o','FSTYPE','-T','/data')=='xfs','data filesystem mismatch')
    no_symlinks(RETAINED);st=RETAINED.stat()
    need(st.st_ino==270239769 and st.st_size==10737418240 and st.st_mtime_ns==1790296419384786773,'quarantined current disk identity changed')
    need(sha(CANDIDATE)==CANDIDATE_SHA,'candidate hash mismatch')
    if a.phase=='before-install':
        need(sha(BINARY)==BASELINE_SHA,'installed baseline hash mismatch')
        need(CONFIG.read_bytes()==a.old_config.read_bytes(),'legacy config differs from escrow')
        for unit in CRITICAL_UNITS:
            need(run('systemctl','show','cube-sandbox-'+unit+'.service','-p','MainPID','--value')=='0','management writer still active')
        for path in Path('/data/cubelet/state').rglob('*'):
            need(not path.is_symlink() and not(path.is_file() and path.suffix=='.db'),'unexpected underlying legacy state')
        need(not Path(ROOT).exists(),'persistent root already exists; inspect instead of reenrolling')
        print(json.dumps({'phase':a.phase,'passed':True,'boot_id':boot,'retained_current_disk':'unchanged stat identity'}));return
    need(sha(BINARY)==CANDIDATE_SHA,'installed candidate mismatch')
    check_config(CONFIG.read_text(),a.old_config.read_text())
    from render_quota import UniqueLoader
    import yaml
    dynamic=yaml.load(Path('/usr/local/services/cubetoolbox/Cubelet/dynamicconf/conf.yaml').read_text(),Loader=UniqueLoader)
    expected_quota={'mcpu_limit':10000,'mem_limit':'10Gi','mvm_limit':128,'creation_concurrent_num':1,'paused_resource_release_ratio':1.0}
    need(dynamic['host']['quota']==expected_quota,'reviewed four-slot worker quota not installed')
    pid=int(run('systemctl','show','cube-sandbox-cubelet.service','-p','MainPID','--value'))
    need(pid>1,'Cubelet not running')
    need(sha(Path('/proc')/str(pid)/'exe')==CANDIDATE_SHA,'running process is not exact candidate')
    paths=[]
    for fd in (Path('/proc')/str(pid)/'fd').iterdir():
        try: paths.append(os.readlink(fd))
        except FileNotFoundError: pass
    counts=check_open_roots(paths)
    filesystems={}
    for _,sub in PLUGINS.values():
        root=Path(ROOT)/sub;no_symlinks(root)
        actual=root if root.exists() else root.parent
        need(root.exists() or sub=='netfile','persistent plugin root absent: '+sub)
        fs=run('nsenter','-t',str(pid),'-m','--','findmnt','-n','-o','FSTYPE','-T',str(actual))
        need(fs=='xfs','plugin actual namespace filesystem not XFS: '+sub)
        filesystems[sub]={'filesystem':fs,'root_exists':root.exists(),'open_databases':counts[sub]}
    need(a.templates is not None,'exact reviewed template manifest required')
    expected=json.loads(a.templates.read_text())
    need(len(expected)==8 and len({row['template_id'] for row in expected})==8,'expected eight distinct reviewed templates')
    for row in expected:
        item=json.loads(run('cubemastercli','tpl','info','--template-id',row['template_id'],'--include-request','--json'))
        check_template(item,row)
    need(pid==int(run('systemctl','show','cube-sandbox-cubelet.service','-p','MainPID','--value')),'Cubelet restarted during check')
    print(json.dumps({'phase':'ready','passed':True,'boot_id':boot,'critical_roots':filesystems,'required_templates':8,'worker_quota':expected_quota,'retained_current_disk':'unchanged stat identity','power_loss_acceptance':False},sort_keys=True))

if __name__=='__main__':
    try: main()
    except (ValueError,KeyError,OSError,subprocess.SubprocessError) as exc:
        # Do not expose subprocess stdout/stderr or private config values.
        print(json.dumps({'passed':False,'error':str(exc) if isinstance(exc,ValueError) else type(exc).__name__}))
        raise SystemExit(1)
