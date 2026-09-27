#!/usr/bin/env python3
"""Operator-side bounded control messages; never transports workspace bytes."""
import concurrent.futures, json, shlex, subprocess, sys, time
from pathlib import Path

ROOT = '/opt/baarcha-bench/cube-fleet-20260927/capacity-100'

def main(config):
    vps, worker = config['vps'], config['worker']
    def remote(code, data=None):
        return subprocess.check_output(vps+['python3 -c '+shlex.quote(code)], input=data, timeout=45, stderr=subprocess.DEVNULL)
    poll = '''import json
from pathlib import Path
r=Path(%r)
p=json.loads((r/'plan.PRIVATE.json').read_text())
jobs=[]
for a in p['apps'][4:]:
 d=r/'guests'/a['id'];f=d/'local-import-job.PRIVATE.json'
 if f.exists() and not any((d/n).exists() for n in ['local-import-result.json','local-import-failure.json']):
  raw=f.read_bytes();assert len(raw)<=32768
  j=json.loads(raw);assert j['app_id']==a['id'];jobs.append(j)
assert len(jobs)<=16
print(json.dumps(dict(jobs=jobs,done=(r/'cleanup.json').exists())))
''' % ROOT
    def run(job):
        raw=json.dumps(job).encode();assert len(raw)<=32768
        completed=subprocess.run(worker+['python3 /root/import-test-copy.py'],input=raw,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=1800)
        assert len(completed.stdout)<=4096
        result=json.loads(completed.stdout)
        if completed.returncode:
            result=dict(app_id=job['app_id'],error=result.get('error','worker import failed'),line=result.get('line'))
            filename='local-import-failure.json'
        else:
            assert result['app_id']==job['app_id'] and result['workspace_sha256']==job['receipt']['workspace_sha256']
            filename='local-import-result.json'
        payload=json.dumps(dict(app_id=job['app_id'],filename=filename,result=result)).encode()
        code='''import json,os,sys
from pathlib import Path
os.umask(0o077);r=Path(%r);v=json.load(sys.stdin)
p=json.loads((r/'plan.PRIVATE.json').read_text());assert v['app_id'] in [a['id'] for a in p['apps']]
assert v['filename'] in ['local-import-result.json','local-import-failure.json']
d=r/'guests'/v['app_id'];f=d/v['filename'];tmp=f.with_suffix('.pending')
tmp.write_text(json.dumps(v['result']));os.replace(tmp,f)
''' % ROOT
        remote(code,payload)
        print(json.dumps(dict(app_id=job['app_id'],result=result)),flush=True)
    seen=set()
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        pending=[]
        while True:
            state=json.loads(remote(poll))
            for job in state['jobs']:
                if job['app_id'] not in seen:
                    seen.add(job['app_id']);pending.append(pool.submit(run,job))
            for future in list(pending):
                if future.done():future.result();pending.remove(future)
            if state['done'] and not pending:break
            time.sleep(3)

if __name__ == '__main__':
    main(json.loads(Path(sys.argv[1]).read_text()))
