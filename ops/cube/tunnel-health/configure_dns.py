#!/usr/bin/env python3
"""Add two isolated tunnel probes; never change project or wildcard routing."""
import json
import urllib.request
from pathlib import Path

MARK = 'Baarcha managed tunnel health probe'
config = json.loads(Path('/etc/baarcha-preview/dns.json').read_text())
assert config['domain'] == 'baarcha.tn'

def api(path, method='GET', body=None):
    req = urllib.request.Request('https://api.cloudflare.com/client/v4' + path,
        data=None if body is None else json.dumps(body).encode(), method=method,
        headers={'Authorization': 'Bearer ' + config['token'], 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=20) as response:
        result = json.load(response)
    assert result['success'], 'Cloudflare rejected the health probe record'
    return result['result']

for worker in ['vps', 'b200-01']:
    name = f'preview-check-{worker}.baarcha.tn'
    body = {'name': name, 'type': 'CNAME', 'content': config['tunnels'][worker]+'.cfargotunnel.com',
            'proxied': True, 'ttl': 1, 'comment': MARK}
    endpoint = '/zones/' + config['zone'] + '/dns_records'
    existing = api(endpoint + '?name=' + name)
    if existing:
        assert len(existing) == 1 and all(existing[0].get(k) == v for k, v in body.items()), 'Health probe record conflict'
    else:
        api(endpoint, 'POST', body)
    print(json.dumps({'worker': worker, 'probe_dns_ready': True}))
