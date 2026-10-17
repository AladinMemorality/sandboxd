#!/usr/bin/env python3
"""Run on the worker with an exact candidate template ID. No model calls.

Creates and deletes only its own journaled canary, never existing sandboxes.
The private API key and returned ingress tokens are not printed.
"""
import http.client
import io
import json
import os
from pathlib import Path
import re
import secrets
import select
import subprocess
import sys
import time
import urllib.parse
import zipfile


def main(template, preset, stage):
    if not re.fullmatch(r'tpl-[a-z0-9]+', template) or preset not in ['react-pro', 'marketplace']:
        raise ValueError('Invalid candidate')
    os.umask(0o077)
    root = Path(stage); root.mkdir(parents=True, exist_ok=False)
    key = json.loads(Path('/root/cube-production/test-secrets.json').read_text())['cube_key']
    supervisor = secrets.token_hex(32)
    def request(port, method, path, headers, value=None):
        connection = http.client.HTTPConnection('127.0.0.1', port, timeout=90)
        try:
            connection.request(method, path, None if value is None else json.dumps(value), {**headers, 'Content-Type': 'application/json'})
            response = connection.getresponse()
            return response.status, response.read(32 * 1024 * 1024)
        finally: connection.close()
    api = {'X-API-Key': key}
    create = {'templateID': template, 'timeout': 300, 'distributionScope': ['10.0.2.15'],
              'envVars': {'RUNTIMED_HTTP_ADDR': ':3031', 'RUNTIMED_HTTP_TOKEN': supervisor},
              'metadata': {'purpose': 'react-base-2-canary', 'run': root.name},
              'lifecycle': {'onTimeout': 'pause', 'autoResume': False}, 'allow_internet_access': False,
              'network': {'allowPublicTraffic': False, 'allowOut': [], 'denyOut': ['0.0.0.0/0']}}
    (root / 'create-intent.PRIVATE.json').write_text(json.dumps(create))
    status, raw = request(3000, 'POST', '/sandboxes', api, create)
    (root / 'create-response.PRIVATE.json').write_bytes(raw)
    if status != 201: raise RuntimeError('Canary creation failed, HTTP ' + str(status))
    (root / 'created.PRIVATE.json').write_bytes(raw)
    sandbox = json.loads(raw); identity = sandbox['sandboxID']
    if not re.fullmatch(r'[A-Za-z0-9_-]+', identity): raise RuntimeError('Unexpected canary identity; reconcile from journal')
    passed = False
    channel = None
    try:
        traffic = {'cube-traffic-access-token': sandbox['trafficAccessToken']}
        runtime = {**traffic, 'Host': f'3031-{identity}.cube.app', 'Authorization': 'Bearer ' + supervisor}
        preview = {**traffic, 'Host': f'3000-{identity}.cube.app'}
        for _ in range(45):
            status, raw = request(80, 'GET', '/status', runtime)
            if status == 200: break
            time.sleep(1)
        if status != 200: raise RuntimeError('Supervisor did not become ready')
        channel = subprocess.Popen([str(Path(__file__).with_name('canary-channel')), str(root)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        readable, _, _ = select.select([channel.stdout], [], [], 30)
        if not readable or channel.stdout.readline() != b'CANARY_CHANNEL_READY\n': raise RuntimeError('Offline broker channel unavailable')
        paths = ['.baarcha/template.json', 'package.json', 'sandbox.yaml', 'src/components/media/BackgroundVideo.tsx', 'src/modules/three/Scene.tsx']
        if preset == 'react-pro': paths += ['STARTER.md', 'src/content/starter.json', 'src/pages/marketing/index.tsx', 'src/pages/portfolio/index.tsx', 'src/pages/app/index.tsx']
        for path in paths:
            status, raw = request(80, 'GET', '/files/content?path=' + urllib.parse.quote(path), runtime)
            if status != 200 or not raw: raise RuntimeError('Prepared canary file unavailable: ' + path)
            if path == '.baarcha/template.json' and preset == 'react-pro' and json.loads(raw)['version'] != '2.0.0': raise RuntimeError('Wrong template revision')
        for _ in range(45):
            if channel.poll() is not None: raise RuntimeError('Canary broker stopped prematurely')
            status, raw = request(80, 'GET', '/', preview)
            if status == 200 and b'/src/main.tsx' in raw: break
            time.sleep(1)
        if status != 200 or b'/src/main.tsx' not in raw:
            (root / 'preview-failure.PRIVATE.txt').write_bytes(raw)
            for endpoint, name in [('/status', 'status'), ('/processes/web/logs', 'web-log')]:
                _, diagnostic = request(80, 'GET', endpoint, runtime)
                (root / (name + '.PRIVATE.txt')).write_bytes(diagnostic)
            raise RuntimeError('Cold preview failed')
        # Vite transforms the actual source; an HTML shell alone is insufficient.
        status, raw = request(80, 'GET', '/src/App.tsx', preview)
        if status != 200 or b'export' not in raw: raise RuntimeError('Application source transform failed')
        if preset == 'react-pro':
            for composition in ['marketing', 'portfolio', 'app']:
                status, raw = request(80, 'GET', '/src/pages/' + composition + '/index.tsx', preview)
                if status != 200 or b'export' not in raw: raise RuntimeError('Prepared page transform failed')
        status, raw = request(80, 'GET', '/export/source', runtime)
        if status != 200: raise RuntimeError('Prepared source export failed')
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            names = archive.namelist()
            for path in ['package.json', 'pnpm-lock.yaml', 'patches/vite@5.4.21.patch', 'src/components/media/BackgroundVideo.tsx']:
                if path not in names: raise RuntimeError('Published source is missing ' + path)
            if any(name.startswith('node_modules/') for name in names): raise RuntimeError('Source export contains dependencies')
            if preset == 'react-pro' and 'src/content/starter.json' not in names: raise RuntimeError('Selected composition lost in publication')
        passed = True
    finally:
        if channel is not None:
            channel.stdin.close()
            try: channel.wait(timeout=10)
            except subprocess.TimeoutExpired: channel.terminate(); channel.wait(timeout=10)
        status, _ = request(3000, 'DELETE', '/sandboxes/' + identity, api)
        deleted = status in [204, 404]
        result = {'template_id': template, 'preset': preset, 'canary_passed': passed and deleted, 'deleted': deleted, 'no_model_calls': True, 'offline': True, 'authenticated_broker': True}
        (root / 'result.json').write_text(json.dumps(result, indent=2))
        print(json.dumps(result), flush=True)
        if not deleted: raise RuntimeError('Canary cleanup needs reconciliation from private journal')


if __name__ == '__main__': main(*sys.argv[1:])
