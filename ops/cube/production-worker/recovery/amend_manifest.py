#!/usr/bin/env python3
"""Append proven original snapshot upper identity without recapturing or changing disks.

Preserve original manifest bytes. Only writes a new private JSON document.
"""
import argparse
import json
from pathlib import Path
from capture import sha256
from plan import require, private_json


def amend(original, plan, original_sha, plan_sha):
    require(original.get('purpose')=='CUBE_CURRENT_DISK_RESCUE_INPUT' and original.get('format')==1,'invalid original manifest')
    require(plan.get('purpose')=='CUBE_CURRENT_DISK_RESCUE' and plan.get('format')==1 and original['sandbox_id']==plan['sandbox_id'],'plan identity mismatch')
    require(not original.get('amendment'),'amendment already exists')
    for key in ('cubebox','storage'):
        require(original['source_metadata_sha256'][key]==plan['source_metadata_sha256'][key],'original metadata binding changed')
    identity=plan.get('upper_identity') or {}
    require(identity.get('kind')=='template_snapshot' and identity.get('container_id')==identity.get('template_id','')+'_0','explicit original snapshot identity required')
    require(identity.get('metadata_sha256')==plan['source_metadata_sha256'].get('template_snapshot_metadata'),'unbound snapshot metadata')
    require(plan['upper_subdir']=='disk/'+identity['container_id']+'/upper','upper path mismatch')
    amended=dict(original)
    amended.update(upper_subdir=plan['upper_subdir'],upper_identity=identity,source_metadata_sha256=plan['source_metadata_sha256'],amendment={'reason':'exact referenced template snapshot original container identity','original_manifest_sha256':original_sha,'verified_plan_sha256':plan_sha})
    return amended

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__)
    for n in ('original','plan','output'):p.add_argument('--'+n,type=Path,required=True)
    a=p.parse_args()
    private_json(a.output,amend(json.loads(a.original.read_text()),json.loads(a.plan.read_text()),sha256(a.original),sha256(a.plan)))
