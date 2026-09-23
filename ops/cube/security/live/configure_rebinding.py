#!/usr/bin/env python3
"""Configure disposable trial DNS and exact per-guest domain policy.

The worker's compiled resolver exception remains UDP53-only. This script adds
that fixed resolver to the normal allow map, whose deny-all rule still applies
to every other destination. No L7 proxy rules are inherited or enabled.
"""
import json, pathlib, socket, struct, subprocess, urllib.request
ROOT=pathlib.Path('/root/cube-pilot/network-live')
assert pathlib.Path('/root/bench-ready').exists()
config=json.loads((ROOT/'config.json').read_text())
maps=json.loads(subprocess.check_output(['bpftool','-j','map','dump','pinned','/sys/fs/bpf/ifindex_to_mvmmeta']))
sibling=json.loads((ROOT/'guest-b.json').read_text())['sandbox_id']
meta=next(x['formatted']['value'] for x in maps if bytes(x['formatted']['value']['uuid']).split(b'\0')[0].decode()==sibling)
address=socket.inet_ntoa(struct.pack('<I',meta['ip']))
config['targets']=[x for x in config['targets'] if not x['domain'].startswith('sibling.')]
config['targets'].append({'ip':address,'domain':'sibling.cube-isolation.test','port':3000})
(ROOT/'config.json').write_text(json.dumps(config,indent=2))
targets=config['targets']
for name in ['a','b']:
    guest=json.loads((ROOT/('guest-'+name+'.json')).read_text())
    body={'allow_internet_access':False,'allowOut':['169.254.254.53/32','registry.npmjs.org']+[x['domain'] for x in targets],'denyOut':['0.0.0.0/0'],'allowPublicTraffic':False}
    request=urllib.request.Request('http://127.0.0.1:3000/sandboxes/'+guest['sandbox_id']+'/network',data=json.dumps(body).encode(),headers={'Content-Type':'application/json','X-API-Key':guest['cube_api_key']},method='PUT')
    with urllib.request.urlopen(request,timeout=30) as response:assert response.status==204
original=(ROOT/'backup/Corefile').read_text()
hosts='    hosts {\n'+''.join('        '+t['ip']+' '+t['domain']+'\n' for t in targets)+'        fallthrough\n    }\n'
pathlib.Path('/usr/local/services/cubetoolbox/coredns/Corefile').write_text(original.replace('    cache 300\n',hosts+'    cache 1\n'))
# Systemd's start script regenerates Corefile; restarting the container keeps
# this explicitly isolated trial configuration in place.
subprocess.run(['docker','restart','cube-proxy-coredns'],check=True,stdout=subprocess.DEVNULL)
print('Configured six exact rebinding targets; management credentials not logged.')
