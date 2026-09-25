#!/usr/bin/env python3
"""Prepare (never install) the reviewed four-slot dynamicconf preserving all else.

Requires PyYAML, already present on the reviewed worker. Refuses duplicate keys.
"""
import argparse
import copy
import os
from pathlib import Path
import re
import yaml

class UniqueLoader(yaml.SafeLoader): pass
def unique_mapping(loader,node,deep=False):
    result={}
    for key_node,value_node in node.value:
        key=loader.construct_object(key_node,deep=deep)
        if key in result: raise ValueError('duplicate YAML key')
        result[key]=loader.construct_object(value_node,deep=deep)
    return result
UniqueLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG,unique_mapping)
EXPECTED={'mcpu_limit':28000,'mem_limit':'30Gi','mvm_limit':128,'creation_concurrent_num':1,'paused_resource_release_ratio':1.0}

def render(text):
    before=yaml.load(text,Loader=UniqueLoader)
    if before['host']['quota']!=EXPECTED:raise ValueError('unexpected previous quota; review instead of overwriting')
    result=text
    for key,value in (('mcpu_limit','10000'),('mem_limit','"10Gi"')):
        pattern=r'(?m)^(\s*'+key+r'\s*:\s*)[^\n#]*(\s*#.*)?$'
        result,count=re.subn(pattern,lambda m:m.group(1)+value+(' '+m.group(2).lstrip() if m.group(2) else ''),result)
        if count!=1:raise ValueError('quota field must occur exactly once')
    after=yaml.load(result,Loader=UniqueLoader)
    expected=copy.deepcopy(before);expected['host']['quota']['mcpu_limit']=10000;expected['host']['quota']['mem_limit']='10Gi'
    if after!=expected:raise ValueError('unintended dynamicconf change')
    return result

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('source',type=Path);p.add_argument('output',type=Path);a=p.parse_args()
    data=render(a.source.read_text())
    fd=os.open(a.output,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as f:f.write(data);f.flush();os.fsync(f.fileno())
