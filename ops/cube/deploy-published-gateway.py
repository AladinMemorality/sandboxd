#!/usr/bin/env python3
"""Install the tested visitor gateway; preserve editor routing and credentials."""
import contextlib, fcntl, json, os, pathlib, shutil, subprocess, time, urllib.request, urllib.error
P = pathlib.Path
ROOT = P('/opt/baarcha-bench/preview-transport-20260929/published')
BIN = P('/opt/baarcha-preview/preview-gateway')
CONFIG = P('/etc/baarcha-preview/gateway.json')
DNS = P('/etc/baarcha-preview/dns.json')
SYNC = P('/opt/baarcha-preview/preview-dns-sync.py')
os.umask(0o077)

def put(path, data, mode=0o600):
    temp = path.with_name(path.name+'.published-new')
    temp.write_bytes(data); temp.chmod(mode); os.replace(temp,path)

def probe(port):
    req=urllib.request.Request('http://127.0.0.1:'+str(port)+'/healthz',headers={'Host':'127.0.0.1:8095'})
    with urllib.request.urlopen(req,timeout=3) as response:
        assert response.status == 200

with contextlib.ExitStack() as stack:
    for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
        f=stack.enter_context(open(name,'a+')); fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
    paths=[BIN,CONFIG,DNS,SYNC]
    before={p:p.read_bytes() for p in paths}
    for p in paths:
        backup=ROOT/(p.name+'.gateway-before')
        assert not backup.exists(),'Gateway release already attempted'
        put(backup,before[p],0o700 if p==BIN else 0o600)
    cfg=json.loads(before[CONFIG]); assert cfg['Worker']=='vps' and cfg['Controller']=='http://127.0.0.1:9090'
    cfg['PublishedController']=cfg['Controller']
    candidate=dict(cfg,Listen='127.0.0.1:18095')
    testConfig=ROOT/'gateway-test.PRIVATE.json'; put(testConfig,json.dumps(candidate).encode())
    proc=subprocess.Popen([str(ROOT/'preview-gateway')],env={**os.environ,'PREVIEW_GATEWAY_CONFIG':str(testConfig)},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    try:
        for attempt in range(30):
            try: probe(18095); break
            except Exception:
                assert proc.poll() is None,'Candidate exited'
                time.sleep(.2)
        else: raise RuntimeError('Candidate failed readiness')
    finally:
        proc.terminate(); proc.wait(timeout=5); testConfig.unlink()
    try:
        put(CONFIG,json.dumps(cfg).encode())
        put(BIN,(ROOT/'preview-gateway').read_bytes(),0o755)
        subprocess.run(['systemctl','restart','baarcha-preview-gateway.service'],check=True)
        probe(8095)
        dns=json.loads(before[DNS]); assert 'vps' in dns['tunnels']; dns['published_worker']='vps'
        put(SYNC,(ROOT.parent/'ops/cube/preview-dns-sync.py').read_bytes(),0o755)
        put(DNS,json.dumps(dns).encode())
        subprocess.run(['systemctl','start','baarcha-preview-dns.service'],check=True)
        result={'gateway_deployed':True,'published_worker':'vps','editor_routing_preserved':True}
        put(ROOT/'gateway-deployed.json',json.dumps(result).encode());print(json.dumps(result))
    except BaseException:
        for p in paths:put(p,before[p],0o755 if p in [BIN,SYNC] else 0o600)
        subprocess.run(['systemctl','restart','baarcha-preview-gateway.service'],check=True)
        probe(8095)
        raise
