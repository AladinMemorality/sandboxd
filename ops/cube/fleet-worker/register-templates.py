#!/usr/bin/env python3
"""Redo only already-verified immutable replicas on the held B200 node."""
import http.client
import json
import os
from pathlib import Path
import time
import uuid


def call(path, value=None):
    c = http.client.HTTPConnection('10.254.240.1',18089,timeout=15)
    try:
        c.request('GET' if value is None else 'POST',path,
                  None if value is None else json.dumps(value),{'Content-Type':'application/json'})
        r=c.getresponse();assert r.status==200
        v=json.loads(r.read(1024*1024));assert v['ret']['ret_code']==200
        return v
    finally:c.close()


def main():
    assert os.geteuid()==0
    manifest=json.loads(Path('/root/cube-fleet-install/artifact-manifest.json').read_text())
    assert len(manifest)==9
    for m in manifest:
        folder=Path('/data/cube-fleet-artifact-cache')/m['artifact_id']
        marker=folder/'verified.json'
        if not marker.exists():
            print(json.dumps({'template':m['template_id'],'pending_cache':True}),flush=True);continue
        ready=json.loads(marker.read_text());s=(folder/'rootfs.ext4').stat()
        assert ready['sha256']==m['ext4_sha256'] and s.st_size==m['ext4_size_bytes']
        assert ready['generation']==[s.st_ino,s.st_size,s.st_mtime_ns]
        path='/cube/template?template_id='+m['template_id']
        def replica():
            entries=[v for v in call(path)['replicas'] if v['node_id']=='10.254.240.2']
            assert len(entries)==1
            return entries[0]
        current=replica()
        if current['status']=='READY':
            assert current['artifact_id']==m['artifact_id'];continue
        result=call('/cube/template/redo',{'template_id':m['template_id'],'distribution_scope':['10.254.240.2'],
                    'failed_only':True,'wait':False,'requestID':str(uuid.uuid4())})
        job=result['job_id'];began=time.monotonic()
        print(json.dumps({'template':m['template_id'],'job':job,'started':True}),flush=True)
        while True:
            current=replica()
            if current.get('last_job_id')==job:
                assert current['status']!='FAILED', 'replica failed; inspect private native logs'
                if current['status']=='READY':
                    assert current['artifact_id']==m['artifact_id']
                    print(json.dumps({'template':m['template_id'],'job':job,'ready':True,'seconds':time.monotonic()-began}),flush=True);break
            assert time.monotonic()-began<240,'template registration deadline exceeded'
            time.sleep(2)


if __name__=='__main__':
    try:main()
    except Exception as error:raise SystemExit('Template registration needs review: '+type(error).__name__)
