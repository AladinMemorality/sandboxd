#!/usr/bin/env python3
"""Explicit fenced stop/restore of the reviewed existing Docker home generations."""
import argparse
import contextlib
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import subprocess
import time
import urllib.request

import capture_roles as roles

LOCKS = (*roles.LOCKS, '/opt/baarcha-bench/cube-workload-operator.lock')
need = roles.need


def run(args, timeout=20):
    if args[0] == 'docker':
        args = ['docker', '--host=unix:///var/run/docker.sock', *args[1:]]
    p = subprocess.run(args, capture_output=True, timeout=timeout)
    need(p.returncode == 0 and len(p.stdout) <= 16 << 20 and len(p.stderr) <= 65536,
         'Fenced Docker operation refused; retain journal and caller locks')
    return p.stdout


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try: os.fsync(fd)
    finally: os.close(fd)


def write(path, data):
    roles.write(path, data)
    sync_dir(path.parent)


def hash_json(value):
    return hashlib.sha256(roles.canonical(value)).hexdigest()


def held_locks(fds):
    need(len(fds) == 4 and len(set(fds)) == 4, 'Four inherited exclusive OFDs required')
    for path, fd in zip(LOCKS, fds):
        p = Path(path)
        need(p.parent.resolve() == p.parent, 'Noncanonical lock parent')
        a, b = os.fstat(fd), p.lstat()
        need(stat.S_ISREG(a.st_mode) and stat.S_ISREG(b.st_mode)
             and a.st_uid == 0 and a.st_nlink == 1 and not a.st_mode & 0o022
             and (a.st_dev, a.st_ino) == (b.st_dev, b.st_ino), 'Wrong inherited lock identity')
        roles.require_inherited_exclusive(path, fd)


def inspect(ids):
    need(ids and all(re.fullmatch('[0-9a-f]{64}', x) for x in ids), 'Full reviewed container IDs required')
    rows = json.loads(run(['docker', 'inspect', *ids]))
    need(len(rows) == len(ids) and {x['Id'] for x in rows} == set(ids), 'Container inventory changed')
    return {x['Id']: x for x in rows}


def fingerprint(c):
    # Do not copy environment values into the journal. Hash the immutable
    # definition, keeping only exact policy/identity fields needed to restore.
    h = dict(c['HostConfig'])
    h.pop('RestartPolicy', None)
    return {'id': c['Id'], 'name': c['Name'], 'image': c['Image'], 'created': c['Created'],
            'definition_sha256': hash_json({'Config': c['Config'], 'HostConfig': h, 'Mounts': c['Mounts']})}


def policy(c):
    p = c['HostConfig']['RestartPolicy']
    need(set(p) == {'Name', 'MaximumRetryCount'} and p['Name'] in ('no', 'always', 'unless-stopped', 'on-failure')
         and type(p['MaximumRetryCount']) is int and 0 <= p['MaximumRetryCount'] <= 100000
         and (p['Name'] == 'on-failure' or p['MaximumRetryCount'] == 0), 'Unreviewed restart policy')
    return p


def stable_state(c):
    s = c['State']
    need(not s.get('Paused') and not s.get('Restarting') and not s.get('Dead')
         and s['Status'] in ('running', 'exited', 'created')
         and ((s['Running'] and s['Status'] == 'running' and s['Pid'] > 0)
              or (not s['Running'] and s['Pid'] == 0)), 'Container is not in a stable running/stopped state')


def same(c, original):
    need(fingerprint(c) == original['identity'], 'Docker definition/generation changed; retain fence')
    stable_state(c)


def overlap(a, b):
    a, b = Path(a), Path(b)
    return a == b or a in b.parents or b in a.parents


def observe(config, allowed_running):
    roles.verify_inputs(config)
    roles.cold_pair.no_open_users([roles.DB, Path(str(roles.DB) + '-wal'), Path(str(roles.DB) + '-shm')])
    with contextlib.closing(sqlite3.connect(roles.DB.as_uri() + '?mode=ro', uri=True)) as db:
        db.execute('BEGIN')
        roles.validate_inventory(roles.database_inventory(db), config['inventory'])
    cp = inspect([config['controller_id']])[config['controller_id']]
    need(cp['Name'] == '/src-sandboxd-1' and cp['Image'] == config['controller_image']
         and hash_json(cp['Config']['Env']) == config['controller_env_sha256']
         and not cp['State']['Running'] and cp['State']['Pid'] == 0
         and policy(cp)['Name'] == 'no', 'Controller is not the exact stopped restart-disabled generation')
    with urllib.request.urlopen('http://127.0.0.1:2019/config/', timeout=3) as response:
        raw = response.read((2 << 20) + 1)
    need(len(raw) <= 2 << 20 and hash_json(json.loads(raw)) == config['reviewed_caddy_sha256'], 'Offline Caddy fence changed')
    roles.route_fence(config)
    for unit in config['stopped_writer_units']:
        need(re.fullmatch(r'baarcha-[a-z0-9-]+\.(?:timer|service)', unit), 'Unexpected writer unit')
        fields = dict(x.split('=', 1) for x in run(['systemctl', 'show', unit, '--property=LoadState', '--property=ActiveState']).decode().splitlines())
        need(fields == {'LoadState': 'loaded', 'ActiveState': 'inactive'}, 'Direct writer is not inactive')
    homes = config['docker_homes']
    need(homes and len({h['container_id'] for h in homes}) == len(homes), 'Exact nonempty home inventory required')
    rows = inspect([h['container_id'] for h in homes])
    recorded = {h['sandbox_id']: h['container_id'] for h in config['inventory']['homes']}
    need(len(recorded) == len(homes) and {h['sandbox_id'] for h in homes} == set(recorded), 'Home inventory incomplete')
    for h in homes:
        c = rows[h['container_id']]
        legacy = recorded[h['sandbox_id']]
        need(legacy == h.get('recorded_container_id', h['container_id'])
             and (legacy == c['Id'] or (re.fullmatch('[0-9a-f]{12}', legacy) and c['Id'].startswith(legacy))), 'Canonical home generation changed')
        need(c['Image'] == h['image'] and len(c['Mounts']) == 1, 'Home image/mount set changed')
        m = c['Mounts'][0]
        need(m['Destination'] == '/home/sandbox' and m['Type'] == 'bind' and m['Source'] == h['source']
             and Path(h['source']).resolve() == Path(h['source']), 'Home path mapping changed')
        stable_state(c)
        need(not c['State']['Running'] or c['Id'] in allowed_running, 'Unexpected running source')
        policy(c)
    paths = [h['source'] for h in homes] + config['role_paths']['rollback-extra'] + config['role_paths']['library-extra'] + [s['image_path'] for s in config['inventory']['snapshots']]
    running = run(['docker', 'ps', '--no-trunc', '-q']).decode().split()
    if running:
        for cid, c in inspect(running).items():
            if cid in allowed_running:
                need(cid in rows, 'Unreviewed writer exception')
                continue
            for mount in c['Mounts']:
                need(not mount.get('RW') or not any(overlap(mount['Source'], p) for p in paths),
                     'Another running container can write a selected archive source')
    return rows


class Journal:
    def __init__(self, directory):
        self.directory = directory
        self.sequence = len(list(directory.glob('[0-9][0-9][0-9][0-9]-*.json')))

    def event(self, phase, **fields):
        self.sequence += 1
        need(self.sequence < 10000, 'Journal event bound exceeded')
        write(self.directory / ('%04d-%s.json' % (self.sequence, phase)),
              {'version': 1, 'at': time.time(), 'phase': phase, **fields})

    def effect(self, action, cid, args, verify):
        self.event('request', action=action, container_id=cid)
        run(args, timeout=180 if action == 'stop' else 60)
        observed = inspect([cid])[cid]
        verify(observed)
        self.event('ack', action=action, container_id=cid)


def restart_arg(p):
    return p['Name'] + (':' + str(p['MaximumRetryCount']) if p['Name'] == 'on-failure' and p['MaximumRetryCount'] else '')


def assert_policy(c, original, expected, running):
    same(c, original)
    need(policy(c) == expected and c['State']['Running'] is running, 'Docker operation did not reach its exact expected state')


def freeze(config, directory, fds):
    need(not directory.exists(), 'Freeze requires a new private journal directory')
    rows = observe(config, {h['container_id'] for h in config['docker_homes']})
    originals = {cid: {'identity': fingerprint(c), 'running': c['State']['Running'], 'restart': policy(c)} for cid, c in rows.items()}
    directory.mkdir(mode=0o700)
    sync_dir(directory.parent)
    write(directory / 'baseline.json', {'version': 1, 'config_sha256': hash_json(config), 'containers': originals})
    journal = Journal(directory)
    remaining = {cid for cid, c in originals.items() if c['running']}
    disabled = set()
    def check_all(current):
        for other, original in originals.items():
            assert_policy(current[other], original,
                          {'Name': 'no', 'MaximumRetryCount': 0} if other in disabled else original['restart'],
                          other in remaining)
    try:
        # Disable every selected source, even already-stopped always-policy
        # containers, before any stop. A daemon restart must not wake them.
        for cid in sorted(originals):
            held_locks(fds)
            current = observe(config, remaining)
            check_all(current)
            original = originals[cid]
            journal.effect('disable-restart', cid, ['docker', 'update', '--restart=no', cid],
                           lambda c: assert_policy(c, original, {'Name': 'no', 'MaximumRetryCount': 0}, original['running']))
            disabled.add(cid)
        for cid in sorted(remaining):
            held_locks(fds)
            current = observe(config, remaining)
            check_all(current)
            original = originals[cid]
            journal.effect('stop', cid, ['docker', 'stop', '--time=-1', cid],
                           lambda c: assert_policy(c, original, {'Name': 'no', 'MaximumRetryCount': 0}, False))
            remaining.remove(cid)
        check_all(observe(config, set()))
        write(directory / 'frozen.json', {'baseline_sha256': roles.sha(directory / 'baseline.json'), 'config_sha256': hash_json(config),
                                         'stopped': sorted(cid for cid, c in originals.items() if c['running']), 'at': time.time()})
    except Exception:
        journal.event('refused', manual_reconciliation_required=True, automatic_restart=False)
        raise


def complete_acknowledgements(directory, expected, all_ids):
    pending = None
    stopped, disabled = set(), set()
    for path in sorted(directory.glob('[0-9][0-9][0-9][0-9]-*.json')):
        item = json.loads(roles.private(path).read_bytes())
        need(item['phase'] in ('request', 'ack'), 'Prior refusal requires manual reconciliation; never replay')
        token = (item['action'], item['container_id'])
        need(token[0] in ('stop', 'disable-restart') and token[1] in all_ids, 'Unexpected freeze operation')
        if item['phase'] == 'request':
            need(pending is None, 'Ambiguous Docker request retained')
            pending = token
        else:
            need(pending == token, 'Unmatched Docker acknowledgement')
            acknowledged = stopped if item['action'] == 'stop' else disabled
            need(item['container_id'] not in acknowledged, 'Repeated freeze operation')
            acknowledged.add(item['container_id'])
            pending = None
    need(pending is None and stopped == expected and disabled == all_ids, 'Incomplete acknowledged freeze')


def restore(config, directory, fds):
    need(not (directory / 'restoring.json').exists() and not (directory / 'restored.json').exists(), 'Restore was already attempted; manual reconciliation required')
    baseline_path = roles.private(directory / 'baseline.json')
    baseline = json.loads(baseline_path.read_bytes())
    frozen = json.loads(roles.private(directory / 'frozen.json').read_bytes())
    need(baseline['config_sha256'] == hash_json(config) == frozen['config_sha256']
         and roles.sha(baseline_path) == frozen['baseline_sha256'], 'Frozen plan/identity changed')
    originals = baseline['containers']
    need(set(originals) == {h['container_id'] for h in config['docker_homes']}, 'Frozen home set changed')
    active = {cid for cid, c in originals.items() if c['running']}
    need(set(frozen['stopped']) == active, 'Frozen acknowledgement differs')
    complete_acknowledgements(directory, active, set(originals))
    current = observe(config, set())
    for cid, original in originals.items():
        assert_policy(current[cid], original, {'Name': 'no', 'MaximumRetryCount': 0}, False)
    write(directory / 'restoring.json', {'at': time.time(), 'baseline_sha256': roles.sha(baseline_path)})
    journal = Journal(directory)
    running, policies_restored = set(), set()
    def check_all(current):
        for other, original in originals.items():
            assert_policy(current[other], original,
                          original['restart'] if other in policies_restored else {'Name': 'no', 'MaximumRetryCount': 0},
                          other in running)
    try:
        for cid in sorted(active):
            held_locks(fds)
            current = observe(config, running)
            check_all(current)
            original = originals[cid]
            journal.effect('start', cid, ['docker', 'start', cid],
                           lambda c: assert_policy(c, original, {'Name': 'no', 'MaximumRetryCount': 0}, True))
            running.add(cid)
        # Restore all original policies only at the end; never start a source
        # that was already stopped in the immutable baseline.
        for cid in sorted(originals):
            held_locks(fds)
            check_all(observe(config, running))
            original = originals[cid]
            journal.effect('restore-restart', cid, ['docker', 'update', '--restart=' + restart_arg(original['restart']), cid],
                           lambda c: assert_policy(c, original, original['restart'], original['running']))
            policies_restored.add(cid)
        current = observe(config, active)
        for cid, original in originals.items(): assert_policy(current[cid], original, original['restart'], original['running'])
        write(directory / 'restored.json', {'at': time.time(), 'restored_ids': sorted(active), 'already_stopped_never_started': sorted(set(originals) - active), 'application_readiness_verified': False})
    except Exception:
        journal.event('refused', manual_reconciliation_required=True, automatic_retry=False)
        raise


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=('freeze', 'restore'))
    p.add_argument('--config', required=True)
    p.add_argument('--config-sha256', required=True)
    p.add_argument('--journal', required=True)
    p.add_argument('--inherited-lock-fds', required=True)
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    need(a.execute, 'Explicit reviewed execution required')
    roles.cold_pair.native_host()
    fds = [int(x) for x in a.inherited_lock_fds.split(',')]
    held_locks(fds)
    path = roles.private(a.config)
    need(roles.sha(path) == a.config_sha256, 'Reviewed roles config changed')
    config = json.loads(path.read_bytes())
    roles.validate_config(config)
    directory = Path(a.journal)
    roles.private(directory.parent, directory=True)
    need(directory.is_absolute() and directory.parent.resolve() == directory.parent and directory.name not in ('.', '..'), 'Private canonical journal path required')
    if a.action == 'restore': roles.private(directory, directory=True)
    with roles.cold_pair.lock_file(str(roles.DB) + '.maintenance.lock', roles.cold_pair.CONTROLLER_MARKER, True):
        {'freeze': freeze, 'restore': restore}[a.action](config, directory, fds)
    print(json.dumps({'action': a.action, 'completed': True, 'application_readiness_verified': False}))


if __name__ == '__main__':
    try: main()
    except Exception: raise SystemExit('Docker-home operation refused. Keep caller locks/routing fence; inspect private journal. No automatic retry or restart.')
