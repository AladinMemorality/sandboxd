#!/usr/bin/env python3
"""Read-only exact-ID metadata escrow and schema validation, usable BEFORE crash.

Never prints metadata, credentials or host source paths. Does not claim fencing.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
from plan import ID, Invalid, make_plan, private_json, require
from capture import no_symlink

def select_storage(text, sandbox_id):
    found=[]
    for line in text.splitlines():
        key,sep,value=line.partition('\t')
        if sep and key==sandbox_id:
            row=json.loads(value)
            require(row.get('sandboxID')==sandbox_id,'storage key/value identity mismatch')
            found.append(row)
    require(len(found)==1,'expected exactly one storage row')
    return found[0]

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--sandbox-id',required=True)
    p.add_argument('--output',required=True,type=Path)
    a=p.parse_args()
    try:
        require(ID.fullmatch(a.sandbox_id) is not None,'invalid ID')
        require(os.geteuid()==0,'operator root required')
        a.output.mkdir(mode=0o700) # refuse existing stage
        box=json.loads(subprocess.check_output(['cubecli','cubebox','inspect',a.sandbox_id],stderr=subprocess.DEVNULL,text=True,timeout=30))
        raw=subprocess.check_output(['cubecli','storage','ls','--raw'],stderr=subprocess.DEVNULL,text=True,timeout=30)
        storage=select_storage(raw,a.sandbox_id)
        private_json(a.output/'cubebox.json',box)
        private_json(a.output/'storage.json',storage)
        snapshot_meta=None
        template=box.get('LocalRunTemplate') or {}
        snapshot=(template.get('snapshot') or {}).get('snapshot') or {}
        if snapshot:
            tid=(template.get('distributionReference') or {}).get('templateID')
            require(isinstance(tid,str) and ID.fullmatch(tid) is not None,'invalid snapshot template ID')
            expected='/data/cubelet/storage/xfs/snapshots/'+tid+'/metadata'
            require(snapshot.get('path')==expected,'unsupported snapshot metadata path')
            source=no_symlink(Path(expected)/'metadata.json')
            require(source.is_file() and 0<source.stat().st_size<=1048576,'invalid template snapshot metadata')
            snapshot_meta=json.loads(source.read_text())
            private_json(a.output/'template-metadata.json',snapshot_meta)
        plan=make_plan(box,storage,a.sandbox_id,snapshot_meta)
        private_json(a.output/'plan.json',plan)
        print(json.dumps({'metadata_schema_valid':True,'sandbox_id':a.sandbox_id,'layers':len(plan['lower_dirs'])+(1 if plan.get('lower_image') else 0),'disk_kind':plan['current_disk']['Kind'],'fencing_verified':False}))
    except Invalid as error:
        p.exit(1,'Metadata schema rejected: '+str(error)+'\n')
    except (OSError,ValueError,TypeError,KeyError,subprocess.SubprocessError):
        p.exit(1,'Metadata schema validation failed; inspect private escrow, do not weaken identity checks.\n')
if __name__=='__main__':main()
