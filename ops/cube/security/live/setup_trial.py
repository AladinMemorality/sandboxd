#!/usr/bin/env python3
"""Create exactly two disposable network guests inside the marked pilot VM.

Stores private credentials locally, never in evidence. This bypasses sandboxd's
domain fail-closed gate only to test a patched worker, not for production use.
"""
import json, os, pathlib, secrets, subprocess, urllib.request

ROOT = pathlib.Path('/root/cube-pilot/network-live')
assert pathlib.Path('/root/bench-ready').exists()
assert not (ROOT / 'guest-a.json').exists(), 'refuse duplicate trial guests'
key = json.loads(pathlib.Path('/root/cube-pilot/test-secrets.json').read_text())['cube_key']
template = json.loads(pathlib.Path('/root/cube-pilot/template-build.json').read_text())['job']['template_id']
targets = [
    {'ip':'10.0.2.15','domain':'worker.cube-isolation.test','port':18081},
    {'ip':'169.254.169.254','domain':'metadata.cube-isolation.test','port':18081},
    {'ip':'169.254.68.5','domain':'gateway.cube-isolation.test','port':18081},
    {'ip':'172.17.0.1','domain':'docker.cube-isolation.test','port':18081},
    {'ip':'65.108.225.153','domain':'public-worker.cube-isolation.test','port':22},
]

def create(name):
    token = secrets.token_hex(32)
    body = {'templateID':template,'envVars':{'RUNTIMED_HTTP_ADDR':':3031','RUNTIMED_HTTP_TOKEN':token},'timeout':7200,
            'lifecycle':{'onTimeout':'pause','autoResume':False},'allow_internet_access':False,
            'network':{'allowPublicTraffic':False,'allowOut':['registry.npmjs.org']+[t['domain'] for t in targets], 'denyOut':['0.0.0.0/0']},
            'metadata':{'purpose':'baarcha-disposable-isolation-'+name}}
    request = urllib.request.Request('http://127.0.0.1:3000/sandboxes',data=json.dumps(body).encode(),
        headers={'Content-Type':'application/json','X-API-Key':key},method='POST')
    with urllib.request.urlopen(request,timeout=120) as response: guest=json.load(response)
    record={'cube_api_key':key,'supervisor_token':token,'traffic_access_token':guest['trafficAccessToken'],'sandbox_id':guest['sandboxID']}
    path=ROOT/('guest-'+name+'.json')
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as out:json.dump(record,out)
    print('created disposable guest',name,guest['sandboxID'])

for name in ['a','b']: create(name)
(ROOT/'config.json').write_text(json.dumps({'targets':targets,'workerIP':'10.0.2.15','tcpPort':18081,'udpPort':18082},indent=2))
corefile=pathlib.Path('/usr/local/services/cubetoolbox/coredns/Corefile')
original=(ROOT/'backup/Corefile').read_text()
hosts='    hosts {\n'+''.join('        '+t['ip']+' '+t['domain']+'\n' for t in targets)+'        fallthrough\n    }\n'
corefile.write_text(original.replace('    cache 300\n',hosts+'    cache 1\n'))
subprocess.run(['docker','restart','cube-proxy-coredns'],check=True)
subprocess.run(['ip','addr','add','169.254.169.254/32','dev','lo'],check=True)
