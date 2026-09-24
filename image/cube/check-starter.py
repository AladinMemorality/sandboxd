#!/usr/bin/env python3
"""Run INSIDE a disposable template image; never against an owner's workspace.

The caller supplies the container/VM resource and network boundary. An already
prepared image is expected. This checks real starter readiness, not VM isolation.
"""
import json
import os
import pathlib
import signal
import subprocess
import sys
import time
import urllib.request

app = pathlib.Path('/home/sandbox/workspace/app')
assert os.getuid() == 1000 and os.getgid() == 1000, 'sandbox identity required'
manifest = (app / 'sandbox.yaml').read_text()
commands = [json.loads(line.split(': ', 1)[1]) for line in manifest.splitlines()
            if line.startswith('  command: ') or line.startswith('    command: ')]
command = next((value for value in commands if value), None)
assert command, 'starter has no process command'
worker = 'web:' not in manifest
health = '/health' if 'health_path: "/health"' in manifest else '/'
began = time.monotonic()
with open('/tmp/starter-output.log', 'wb') as log:
    process = subprocess.Popen(['bash', '-c', command], cwd=app,
                               stdout=log, stderr=log, start_new_session=True)
    try:
        deadline = began + 45
        while time.monotonic() < deadline:
            assert process.poll() is None, 'starter exited; inspect private fixture log'
            if worker:
                if time.monotonic() - began > 2:
                    break
            else:
                try:
                    with urllib.request.urlopen('http://127.0.0.1:3000' + health, timeout=2) as response:
                        assert response.status == 200
                        assert response.read(1024), 'empty starter response'
                        break
                except (OSError, urllib.error.URLError):
                    pass
            time.sleep(.1)
        else:
            raise RuntimeError('starter readiness deadline exceeded')
        print(json.dumps({'preset': os.environ.get('RUNTIMED_RUNTIME_PRESET'),
                          'ready_ms': round((time.monotonic() - began) * 1000),
                          'worker': worker, 'uid': os.getuid()}), flush=True)
    except Exception:
        # Only this credential-free starter fixture may use this diagnostic;
        # never point it at an owner's app or publish owner process output.
        print(pathlib.Path('/tmp/starter-output.log').read_text(errors='replace')[-4000:], file=sys.stderr)
        raise
    finally:
        try:
            os.killpg(process.pid, signal.SIGTERM)
            process.wait(timeout=5)
        except ProcessLookupError:
            pass
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
