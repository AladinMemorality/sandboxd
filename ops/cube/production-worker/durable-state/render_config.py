#!/usr/bin/env python3
"""Render a private reviewed durable-metadata config; never install or start it."""
import argparse
import copy
import os
from pathlib import Path
import re
import tomllib
ROOT='/data/cubelet/persistent-metadata'
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
'io.containerd.snapshotter.v1.overlayfs':('root_path','overlayfs'),
}
def render(text):
    before=tomllib.loads(text)
    if before.get('state')!='/data/cubelet/state' or before.get('durable_metadata_root'):
        raise ValueError('expected reviewed legacy config, not already migrated or another state tree')
    current=None;seen=set();lines=[]
    for line in text.splitlines():
        if line.lstrip().startswith('['):
            match=re.fullmatch(r'\s*\[plugins\."([^"]+)"\]\s*(?:#.*)?',line)
            current=match.group(1) if match else None
            lines.append(line)
            if current in PLUGINS:
                if current in seen:raise ValueError('duplicate plugin section')
                seen.add(current);key,subdir=PLUGINS[current]
                lines.append(f'    {key} = "{ROOT}/{subdir}"')
                if current=='io.containerd.metadata.v1.bolt':lines.append('    no_sync = false')
            continue
        if current in PLUGINS:
            key=PLUGINS[current][0]
            if re.match(r'\s*'+key+r'\s*=',line) or current=='io.containerd.metadata.v1.bolt' and re.match(r'\s*no_sync\s*=',line):continue
        lines.append(line)
    for plugin in PLUGINS.keys()-seen:
        key,subdir=PLUGINS[plugin];lines.extend([f'[plugins."{plugin}"]',f'  {key} = "{ROOT}/{subdir}"'])
        if plugin=='io.containerd.metadata.v1.bolt':lines.append('  no_sync = false')
    result=f'durable_metadata_root = "{ROOT}"\n'+'\n'.join(lines)+'\n'
    after=tomllib.loads(result)
    expected=copy.deepcopy(before);expected['durable_metadata_root']=ROOT
    for plugin,(key,subdir) in PLUGINS.items():expected.setdefault('plugins',{}).setdefault(plugin,{})[key]=f'{ROOT}/{subdir}'
    expected['plugins']['io.containerd.metadata.v1.bolt']['no_sync']=False
    if after!=expected:raise ValueError('unintended config change')
    return result
if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('source',type=Path);p.add_argument('output',type=Path);a=p.parse_args()
    data=render(a.source.read_text())
    fd=os.open(a.output,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as f:f.write(data);f.flush();os.fsync(f.fileno())
