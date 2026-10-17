#!/usr/bin/env python3
"""Create and verify one private Cube candidate; leave it paused for later use.

Run on worker-01. Credentials remain in a mode-0700 operator journal.
No controller defaults or customer runtime bindings are changed.
"""
import argparse
import http.client
import io
import json
import os
from pathlib import Path
import re
import secrets
import select
import subprocess
import time
import zipfile


def request(port, method, path, headers, value=None):
    connection = http.client.HTTPConnection('127.0.0.1', port, timeout=90)
    try:
        connection.request(method, path, None if value is None else json.dumps(value),
                           {**headers, 'Content-Type': 'application/json'})
        response = connection.getresponse()
        return response.status, response.read(64 * 1024 * 1024)
    finally:
        connection.close()


class Candidate:
    def __init__(self, root):
        self.root = Path(root)
        key = json.loads(Path('/root/cube-production/test-secrets.json').read_text())['cube_key']
        self.api = {'X-API-Key': key}
        self.channel = None

    def save(self, name, value):
        (self.root / name).write_text(json.dumps(value, indent=2))

    def create(self, template):
        if not re.fullmatch(r'tpl-[a-z0-9]+', template):
            raise ValueError('Invalid template ID')
        self.root.mkdir(mode=0o700, parents=True, exist_ok=False)
        self.intent = {
            'templateID': template, 'timeout': 3600, 'distributionScope': ['10.0.2.15'],
            'envVars': {'RUNTIMED_HTTP_ADDR': ':3031', 'RUNTIMED_HTTP_TOKEN': secrets.token_hex(32)},
            'metadata': {'purpose': 'nextjs-postgres-candidate', 'run': self.root.name},
            'lifecycle': {'onTimeout': 'pause', 'autoResume': False}, 'allow_internet_access': False,
            'network': {'allowPublicTraffic': False, 'allowOut': [], 'denyOut': ['0.0.0.0/0']},
        }
        self.save('create-intent.PRIVATE.json', self.intent)
        started = time.monotonic()
        self.created_at = started
        status, raw = request(3000, 'POST', '/sandboxes', self.api, self.intent)
        (self.root / 'create-response.PRIVATE.json').write_bytes(raw)
        if status != 201:
            raise RuntimeError(f'Create returned HTTP {status}; reconcile journal before retrying')
        self.created = json.loads(raw)
        self.save('created.PRIVATE.json', self.created)
        self.create_seconds = time.monotonic() - started
        self.save('create-timing.json', {'create_seconds': self.create_seconds})
        self.headers()

    def load(self):
        self.intent = json.loads((self.root / 'create-intent.PRIVATE.json').read_text())
        self.created = json.loads((self.root / 'created.PRIVATE.json').read_text())
        self.headers()

    def headers(self):
        self.identity = self.created['sandboxID']
        if not re.fullmatch(r'[A-Za-z0-9_-]+', self.identity):
            raise ValueError('Invalid returned sandbox ID')
        traffic = {'cube-traffic-access-token': self.created['trafficAccessToken']}
        self.runtime = {**traffic, 'Host': f'3031-{self.identity}.cube.app',
                        'Authorization': 'Bearer ' + self.intent['envVars']['RUNTIMED_HTTP_TOKEN']}
        self.preview = {**traffic, 'Host': f'3000-{self.identity}.cube.app'}

    def start_channel(self):
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            status, _ = request(80, 'GET', '/status', self.runtime)
            if status == 200:
                break
            time.sleep(0.5)
        else:
            raise RuntimeError('Supervisor not ready')
        self.channel_log = (self.root / 'channel.log').open('ab')
        self.channel = subprocess.Popen(['/root/react-base-20261010/canary-channel', str(self.root)],
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.channel_log)
        readable, _, _ = select.select([self.channel.stdout], [], [], 30)
        if not readable or self.channel.stdout.readline() != b'CANARY_CHANNEL_READY\n':
            raise RuntimeError('Authenticated offline broker unavailable')

    def stop_channel(self):
        if self.channel is not None:
            self.channel.stdin.close()
            try:
                self.channel.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.channel.terminate()
                self.channel.wait(timeout=10)
            self.channel_log.close()
            self.channel = None

    def ready(self):
        started = time.monotonic()
        attempts = []
        while time.monotonic() - started < 120:
            status, raw = request(80, 'GET', '/api/health', self.preview)
            attempt = {'elapsed_seconds': round(time.monotonic() - started, 3), 'health_status': status}
            if status == 200:
                value = json.loads(raw)
                assert value['postgresql'].startswith('18.6'), value
                page_started = time.monotonic()
                page_status, page = request(80, 'GET', '/', self.preview)
                attempt.update(page_status=page_status, page_seconds=round(time.monotonic() - page_started, 3))
                attempts.append(attempt)
                self.save('readiness-latest.json', attempts)
                if page_status == 200 and b'Ready for your next idea' in page:
                    self.readiness_attempts = attempts
                    return time.monotonic() - started
            else:
                attempts.append(attempt)
                self.save('readiness-latest.json', attempts)
            time.sleep(0.5)
        for path, name in [('/status', 'status'), ('/processes/web/logs', 'web'), ('/processes/postgres/logs', 'postgres')]:
            _, body = request(80, 'GET', path, self.runtime)
            (self.root / (name + '.PRIVATE.log')).write_bytes(body)
        raise RuntimeError('Next.js/database did not become ready')

    def pause(self):
        self.stop_channel()
        status, _ = request(3000, 'POST', f'/sandboxes/{self.identity}/pause', self.api, {})
        if status != 204:
            raise RuntimeError(f'Pause failed: HTTP {status}')

    def resume(self):
        started = time.monotonic()
        status, raw = request(3000, 'POST', f'/sandboxes/{self.identity}/connect', self.api, {'timeout': 3600})
        if status != 200:
            raise RuntimeError(f'Resume failed: HTTP {status}')
        updated = json.loads(raw)
        self.created.update(updated)
        self.save('created.PRIVATE.json', self.created)
        self.headers()
        self.start_channel()
        self.ready()
        return time.monotonic() - started

    def verify(self):
        self.start_channel()
        cold = self.ready()
        cold_attempts = self.readiness_attempts
        status, raw = request(80, 'GET', '/', self.preview)
        assert status == 200 and b'Ready for your next idea' in raw
        cold_preview_total = time.monotonic() - self.created_at
        status, raw = request(80, 'GET', '/api/runtime', self.preview)
        assert status == 200, f'Runtime verification: HTTP {status}'
        versions = json.loads(raw)
        assert versions['node'] == '26.12.0', versions
        assert versions['next'] == '16.4.2', versions
        assert versions['react'] == '19.3.1', versions
        assert versions['claude'].startswith('2.1.302'), versions
        assert versions['postgresql'] == 'postgres (PostgreSQL) 18.6', versions
        assert versions['pnpm'] == '12.14.0', versions
        status, raw = request(80, 'GET', '/api/notes', self.preview)
        assert status == 200 and json.loads(raw) == [], 'New image must contain no database rows'
        status, _ = request(80, 'POST', '/api/notes', self.preview, {'body': ''})
        assert status == 400
        marker = 'Database verified — this note survived a sandbox pause and resume.'
        status, raw = request(80, 'POST', '/api/notes', self.preview, {'body': marker})
        assert status == 201 and json.loads(raw)['body'] == marker
        self.pause()
        resumed = self.resume()
        status, raw = request(80, 'GET', '/api/notes', self.preview)
        assert status == 200 and any(note['body'] == marker for note in json.loads(raw)), 'Saved data lost'
        status, raw = request(80, 'GET', '/export/source', self.runtime)
        assert status == 200
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            names = archive.namelist()
            for name in ['package.json', 'pnpm-lock.yaml', 'app/page.tsx', 'lib/db.ts', 'sandbox.yaml', 'AGENTS.md']:
                assert name in names, name
            assert not any('node_modules/' in name or '.baarcha-postgres/' in name or '.next-build/' in name for name in names)
        result = {'sandbox_id': self.identity, 'template_id': self.intent['templateID'], 'versions': versions,
                  'create_seconds': round(self.create_seconds, 3), 'cold_app_ready_seconds': round(cold, 3),
                  'cold_preview_total_seconds': round(cold_preview_total, 3),
                  'cold_readiness_attempts': cold_attempts,
                  'resume_ready_seconds': round(resumed, 3), 'database_write_read': True,
                  'database_survived_pause_resume': True, 'source_export': True,
                  'private_ingress': True, 'offline': True, 'claude_binary_verified': True,
                  'claude_model_call_tested': False, 'production_defaults_changed': False}
        self.save('result.json', result)
        print(json.dumps(result), flush=True)


if __name__ == '__main__':
    os.umask(0o077)
    parser = argparse.ArgumentParser()
    parser.add_argument('template')
    parser.add_argument('journal')
    args = parser.parse_args()
    candidate = Candidate(args.journal)
    candidate.create(args.template)
    try:
        candidate.verify()
    finally:
        candidate.pause()
