#!/usr/bin/env python3
"""Carry only bounded migration jobs/proofs; archives go directly through S3."""
import concurrent.futures,json,shlex,subprocess,sys,time
from pathlib import Path
ROOT='/opt/baarcha-bench/cube-fleet-20260927/capacity-ready/moves'
def main(config):
    vps,worker=config['vps'],config['worker']
    def remote(code,body=None):return subprocess.check_output(vps+['python3 -c '+shlex.quote(code)],input=body,stderr=subprocess.DEVNULL,timeout=40)
    source=Path(__file__).with_name('move-project-worker.py').read_bytes()
    subprocess.run(worker+['umask 077; cat > /root/move-project-worker.py'],input=source,check=True,stdout=subprocess.DEVNULL)
    poll='''import json\nfrom pathlib import Path\nr=Path(%r)\nout=[]\nfor p in r.glob('*/worker-job.PRIVATE.json'):\n if not (p.parent/'worker-result.json').exists():\n  assert p.stat().st_size<=262144\n  out.append(json.loads(p.read_text()))\nassert len(out)<=16\nprint(json.dumps(out))\n'''%ROOT
    def run(job):
        payload=json.dumps(job).encode();assert len(payload)<=262144
        p=subprocess.Popen(worker+['python3 /root/move-project-worker.py'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
        p.stdin.write(payload);p.stdin.close()
        for line in p.stdout:
            assert len(line)<=262144
            result=json.loads(line)
            name='worker-content.json' if result.get('stage')=='content-verified' else 'worker-result.json'
            code='''import json,os,re,sys\nfrom pathlib import Path\nos.umask(0o077);v=json.load(sys.stdin);assert re.fullmatch('[a-zA-Z0-9_-]{1,128}',v['id'])\np=Path(%r)/v['id'];j=json.loads((p/'worker-job.PRIVATE.json').read_text());assert j['id']==v['id']\nassert v['name'] in ['worker-content.json','worker-result.json']\nt=p/(v['name']+'.pending');t.write_text(json.dumps(v['result']));os.replace(t,p/v['name'])\n'''%ROOT
            remote(code,json.dumps(dict(id=job['id'],result=result,name=name)).encode())
        rc=p.wait(timeout=30)
        if rc:assert result.get('failed')
        print(json.dumps(dict(id=job['id'],failed=bool(rc))),flush=True)
    seen=set()
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        pending=[]
        while True:
            for job in json.loads(remote(poll)):
                if job['id'] not in seen:seen.add(job['id']);pending.append(pool.submit(run,job))
            for p in list(pending):
                if p.done():p.result();pending.remove(p)
            time.sleep(2)
if __name__=='__main__':main(json.loads(Path(sys.argv[1]).read_text()))
