#!/usr/bin/env python3
"""Offline amd64 checks before registering the candidate with Cube."""
import json
from pathlib import Path
import subprocess
import time

ROOT = Path('/opt/baarcha-bench/nextjs-postgres-20261017')
IMAGE = 'baarcha-cube-nextjs-postgres:20261017'


def run(*args, **kwargs):
    return subprocess.check_output(list(args), text=True, **kwargs).strip()


def main():
    image = run('docker', 'image', 'inspect', IMAGE, '--format', '{{.Id}}')
    command = '''cd /home/sandbox/workspace/app
node /opt/services/postgres/worker.mjs & database=$!
pnpm dev & web=$!
trap 'kill -TERM "$web" "$database" 2>/dev/null; wait' TERM INT
wait
'''
    container = run('docker', 'run', '-d', '--network', 'none', '--cpus', '2', '--memory', '4g',
                    '--entrypoint', '/bin/bash', IMAGE, '-lc', command)
    (ROOT / 'docker-smoke-container.json').write_text(json.dumps({'id': container, 'image': image}))

    def http(path, data=None):
        args = ['docker', 'exec', container, 'curl', '-sS', '--max-time', '35', '-w', '\n%{http_code}',
                'http://127.0.0.1:3000' + path]
        if data is not None:
            args += ['-H', 'Content-Type: application/json', '--data-binary', json.dumps(data)]
        p = subprocess.run(args, text=True, capture_output=True, timeout=45)
        if p.returncode:
            return 0, p.stderr
        body, status = p.stdout.rsplit('\n', 1)
        return int(status), body

    def ready():
        started = time.monotonic()
        while time.monotonic() - started < 120:
            status, body = http('/api/health')
            if status == 200:
                assert json.loads(body)['postgresql'].startswith('18.6')
                return round(time.monotonic() - started, 3)
            time.sleep(1)
        raise RuntimeError('Offline app/database failed to become ready')

    try:
        cold = ready()
        status, body = http('/api/runtime')
        assert status == 200, body
        versions = json.loads(body)
        assert versions == {'node': '26.12.0', 'next': '16.4.2', 'react': '19.3.1',
                            'claude': '2.1.302 (Claude Code)', 'postgresql': 'postgres (PostgreSQL) 18.6', 'pnpm': '12.14.0'}, versions
        status, body = http('/')
        assert status == 200 and 'Ready for your next idea' in body
        status, body = http('/api/notes')
        assert status == 200 and json.loads(body) == []
        status, body = http('/api/notes', {'body': "Persistence check '); --"})
        assert status == 201
        note = json.loads(body)
        assert http('/api/notes', {'body': ''})[0] == 400
        assert http('/api/notes', {'body': 'x' * 501})[0] == 400
        # Check a production build while the development server remains active.
        with (ROOT / 'concurrent-build.log').open('w') as log:
            subprocess.run(['docker', 'exec', '-w', '/home/sandbox/workspace/app', container,
                            'pnpm', 'build'], stdout=log, stderr=subprocess.STDOUT, check=True, timeout=180)
        assert http('/api/health')[0] == 200
        run('docker', 'restart', '-t', '20', container, timeout=45)
        restart = ready()
        status, body = http('/api/notes')
        assert status == 200 and note in json.loads(body), 'PostgreSQL data lost on process restart'
        result = {'image': image, 'versions': versions, 'offline': True, 'cpu_count': 2, 'memory_mb': 4096,
                  'cold_app_ready_seconds': cold, 'restart_ready_seconds': restart,
                  'database_survived_process_restart': True, 'build_preserved_live_preview': True}
        (ROOT / 'docker-smoke.json').write_text(json.dumps(result, indent=2))
        (ROOT / 'nextjs-postgres-image.json').write_text(json.dumps({
            'preset': 'nextjs-postgres', 'tag': IMAGE, 'image': image, 'validation': result}, indent=2))
        print(json.dumps(result), flush=True)
    finally:
        logs = subprocess.run(['docker', 'logs', container], text=True, capture_output=True)
        (ROOT / 'docker-smoke.log').write_text(logs.stdout + logs.stderr)
        subprocess.run(['docker', 'rm', '-f', container], check=True, capture_output=True)


if __name__ == '__main__':
    main()
