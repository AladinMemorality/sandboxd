#!/usr/bin/env python3
"""Apply only the verified React template defaults; retain exact rollback state."""
import contextlib
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import time
import urllib.request

from plan import plan, budget_contracts

ROOT = Path('/opt/baarcha-bench/react-base-20261010')
FILES = [Path('/opt/sandboxd/deploy-state/runtime-compose.json'), Path('/opt/sandboxd/deploy-state/active-images.json')]
STOP = Path('/etc/baarcha-cube/worker-stop.json')
NAMES = ['SANDBOXD_CUBE_TEMPLATES', 'SANDBOXD_CUBE_ADMISSION', 'SANDBOXD_CUBE_FLEET']
LOCKS = ['/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock', '/run/lock/cube-operator-acceptance.lock', '/opt/baarcha-bench/cube-workload-operator.lock']

def run(args): return subprocess.check_output(args, stderr=subprocess.STDOUT)
def inspect(): return json.loads(run(['docker', 'inspect', 'src-sandboxd-1']))[0]
def env(container): return dict(x.split('=', 1) for x in container['Config']['Env'])
def compose(*args):
    return run(['docker', 'compose', '--project-directory', '/opt/sandboxd/src', '-f', '/opt/sandboxd/src/docker-compose.yml', '-f', str(FILES[0]), '-f', str(FILES[1]), *args])
def put(path, value):
    temporary = path.with_name(path.name + '.react-base-new')
    temporary.write_bytes(value); temporary.chmod(0o600); os.replace(temporary, path)
def write(path, value): put(path, json.dumps(value, indent=2).encode())
def db(): return sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro', uri=True, timeout=10)
def bindings():
    with contextlib.closing(db()) as connection:
        return connection.execute('select sandbox_id,runtime_id,template_id from runtime_binding order by sandbox_id').fetchall()
def contracts():
    with contextlib.closing(db()) as connection:
        return dict(connection.execute('select worker_id,contract from cube_resource_budget'))
def migrate_contracts(before, after):
    # Controller is stopped first. Use its canonical maintenance inode and an
    # exclusive transaction; never restore a whole database over newer state.
    with open('/var/lib/sandboxd/state/sandboxd.db.maintenance.lock', 'r+') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        assert lock.read() == 'sandboxd-offline-maintenance-v1\n'
        with sqlite3.connect('/var/lib/sandboxd/state/sandboxd.db', timeout=10) as connection:
            connection.execute('BEGIN IMMEDIATE')
            assert dict(connection.execute('select worker_id,contract from cube_resource_budget')) == before
            for worker, value in after.items():
                assert connection.execute('update cube_resource_budget set contract=? where worker_id=? and contract=?', (value, worker, before[worker])).rowcount == 1
def idle():
    with contextlib.closing(db()) as connection:
        assert connection.execute("select count(*) from task where status not in ('succeeded','failed','cancelled','canceled')").fetchone()[0] == 0, 'Active task; retry later'
        assert connection.execute("select count(*) from cube_admission where state='pending'").fetchone()[0] == 0, 'Provider operation pending; retry later'
def ready():
    for _ in range(60):
        try:
            with urllib.request.urlopen('http://127.0.0.1:9090/readyz', timeout=3) as response:
                if response.status == 200: return
        except Exception: time.sleep(1)
    raise RuntimeError('Controller readiness failed')
def recreate():
    compose('up', '-d', '--no-deps', '--no-build', '--pull', 'never', 'sandboxd')
    compose('up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--force-recreate', 'cube-management-api', 'cube-management-proxy', 'cube-management-master', 'cube-management-b200-proxy')
    ready()

def main():
    assert len(sys.argv) == 2 and sys.argv[1] in ['--prepare', '--apply']
    os.umask(0o077)
    with contextlib.ExitStack() as stack:
        for name in LOCKS:
            stream = stack.enter_context(open(name, 'a+')); fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        before = inspect(); environment = {key: env(before)[key] for key in NAMES}
        assert before['State']['Running']
        assert json.loads(STOP.read_bytes())['controller_id'] == before['Id'], 'Lifecycle pin mismatch'
        idle(); ready()
        if sys.argv[1] == '--prepare':
            assert not (ROOT / 'activation-baseline.json').exists(), 'Review existing baseline before replanning'
            replacements = {}
            previous = json.loads(environment[NAMES[0]])
            for preset in ['react-pro', 'marketplace']:
                registered = json.loads((ROOT / (preset + '-ready.json')).read_bytes())
                tested = json.loads((ROOT / (preset + '-canary.json')).read_bytes())
                assert registered['template_id'] == tested['template_id'] and tested['canary_passed'] and tested['deleted'] and tested['authenticated_broker']
                replacements[preset] = {'previous_template': previous[preset], 'template_id': registered['template_id'], 'ready_nodes': registered['ready_nodes'], 'canary_passed': True, 'cpu_count': 2, 'memory_mb': 2048, 'writable_disk_mb': 10240}
            release = {'replacements': replacements}
            overlay = plan(environment, release)
            durable = contracts()
            revised = budget_contracts(environment, overlay['services']['sandboxd']['environment'], durable)
            write(ROOT / 'activation-baseline.json', {'image': before['Image'], 'environment': environment, 'contracts': durable})
            write(ROOT / 'durable-contracts.json', revised)
            write(ROOT / 'verified-release.json', release); write(ROOT / 'overlay.json', overlay)
            print(json.dumps({'planned': True, 'replacements': replacements})); return
        baseline = json.loads((ROOT / 'activation-baseline.json').read_bytes())
        assert before['Image'] == baseline['image'] and environment == baseline['environment'], 'Live baseline changed'
        release = json.loads((ROOT / 'verified-release.json').read_bytes())
        overlay = json.loads((ROOT / 'overlay.json').read_bytes())
        assert overlay == plan(environment, release), 'Overlay changed'
        candidate = overlay['services']['sandboxd']['environment']
        old_contracts = baseline['contracts']
        new_contracts = budget_contracts(environment, candidate, old_contracts)
        assert contracts() == old_contracts
        assert json.loads((ROOT / 'durable-contracts.json').read_bytes()) == new_contracts
        assert not (ROOT / 'activation-backup.PRIVATE.sqlite').exists(), 'Previous attempt exists; reconcile before retry'
        with contextlib.closing(db()) as connection, sqlite3.connect(ROOT / 'activation-backup.PRIVATE.sqlite') as backup: connection.backup(backup)
        prior_bindings = bindings()
        original = {path: path.read_bytes() for path in [*FILES, STOP]}
        for path, data in original.items(): put(ROOT / (path.name + '.before'), data)
        rendered = json.loads(compose('config', '--format', 'json'))
        assert all(rendered['services']['sandboxd']['environment'][key] == value for key, value in environment.items())
        migrated = False
        try:
            for path in FILES:
                value = json.loads(original[path]); value['services']['sandboxd'].setdefault('environment', {}).update(candidate); write(path, value)
            expected = json.loads(json.dumps(rendered)); expected['services']['sandboxd']['environment'].update(candidate)
            assert json.loads(compose('config', '--format', 'json')) == expected, 'Unexpected Compose changes'
            idle()
            compose('stop', 'sandboxd')
            migrate_contracts(old_contracts, new_contracts); migrated = True
            recreate()
            after = inspect()
            assert after['Image'] == before['Image'] and all(env(after)[key] == value for key, value in candidate.items())
            pin = json.loads(original[STOP]); pin['controller_id'] = after['Id']; write(STOP, pin)
            assert bindings() == prior_bindings, 'Existing runtime bindings changed'
            assert contracts() == new_contracts
            result = {'deployed': True, 'controller_image_unchanged': True, 'bindings_preserved': len(prior_bindings), 'durable_budget_extended_without_resize': True, 'defaults': json.loads(candidate[NAMES[0]]), 'replacements': release['replacements']}
            write(ROOT / 'activated.json', result); print(json.dumps(result))
        except BaseException:
            # Save failure details privately before recreating the container.
            import traceback
            put(ROOT / 'activation-error.PRIVATE.txt', traceback.format_exc().encode())
            if migrated:
                compose('stop', 'sandboxd')
                migrate_contracts(new_contracts, old_contracts)
            for path in FILES: put(path, original[path])
            recreate()
            pin = json.loads(original[STOP]); pin['controller_id'] = inspect()['Id']; write(STOP, pin)
            print('Restored previous controller configuration', flush=True)
            raise

if __name__ == '__main__': main()
