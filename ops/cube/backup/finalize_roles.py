#!/usr/bin/env python3
"""Close new startup configuration after a genuine stop; preserve heavy roles."""
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys
import tarfile
import time

import capture_roles as roles
import cold_pair

CONFIG_ROLES = ('controller-config', 'worker-config', 'worker-launch')
HEAVY_ROLES = ('rollback', 'library', 'platform-db')
START = '/etc/baarcha-cube/worker-start.json'
EXTERNAL_VALIDATOR = '/usr/local/libexec/baarcha-cube-external-clean.py'
WORKER = {'worker_unit': 'baarcha-cube-worker-01.service',
          'worker_lock': '/opt/baarcha-cube/worker-01/backup.lock'}


def read(path):
    path = roles.private(path)
    roles.need(path.stat().st_size <= 16 * 1024 * 1024, 'Oversized private receipt')
    return json.loads(path.read_text())


def covered(path, roots):
    return any(Path(path) == Path(root) or Path(root) in Path(path).parents for root in roots)


def validate_transition(before, after, pause, clean, evidence=()):
    """Only startup receipt selection and its archived evidence may change."""
    roles.validate_config(before)
    roles.validate_config(after)
    left, right = copy.deepcopy(before), copy.deepcopy(after)
    old_files, new_files = left.pop('reviewed_files'), right.pop('reviewed_files')
    old_paths, new_paths = left.pop('role_paths'), right.pop('role_paths')
    roles.need(left == right, 'Closed source inventory or policy changed')
    roles.need(START in old_files and START in new_files, 'Startup config must be pinned in both generations')
    additions = {pause, clean} | {item['path'] for item in evidence}
    roles.need(set(new_files) == set(old_files) | additions, 'Only exact stop receipts may be added')
    for item in evidence:
        roles.need(new_files.get(item['path']) == item['sha256'], 'External evidence hash is not pinned')
    for path, digest in old_files.items():
        if path != START:
            roles.need(new_files.get(path) == digest, 'Unrelated configuration changed after role closure')
    for role, paths in old_paths.items():
        expected = set(paths) | (additions if role == 'worker-config' else set())
        roles.need(set(new_paths[role]) == expected and len(new_paths[role]) == len(set(new_paths[role])),
                   'Only exact worker receipt archive entries may be added')
    roles.need(covered(START, new_paths['worker-config']), 'Startup config missing from worker role')


def validate_receipts(start, pause_path, clean_path, pause, clean, external=None):
    expected_start = {'version': 1, 'pause_proof': pause_path, 'clean_receipt': clean_path}
    if external is not None:
        expected_start['external_verifier_sha256'] = external['validator_sha256']
    roles.need(start == expected_start,
               'Installed startup config does not select this exact stop')
    roles.need(pause.get('version') == 1 and pause.get('verified') is True and pause.get('provider_jobs') == 0,
               'Actual verified native pause proof required')
    accepted = clean.get('state') == 'stopped-clean'
    if clean.get('state') == 'externally-stopped-clean':
        accepted = external is not None and external.get('receipt') == clean and bool(external.get('closure'))
    roles.need(clean.get('version') == 1 and accepted and clean.get('proof') == pause,
               'Matching actual clean-stop receipt required')
    # Freshness and exact SQLite binding/admission comparison are performed by
    # cold_pair.validate_pause_receipt, both before and after archiving.
    from datetime import datetime
    generated = datetime.fromisoformat(pause['generated_at'])
    roles.need(generated.tzinfo is not None and generated.timestamp() <= clean.get('generated_at', 0) <= time.time() + 1,
               'Clean-stop receipt chronology invalid')


def external_evidence(before, config, receipt_path, receipt):
    if receipt.get('state') != 'externally-stopped-clean':
        return None
    # This fixed reviewed validator must already be present in the first closed
    # configuration. Never import a helper named by the receipt or caller.
    expected = before['reviewed_files'].get(EXTERNAL_VALIDATOR)
    roles.need(expected is not None and config['reviewed_files'].get(EXTERNAL_VALIDATOR) == expected,
               'External validator must be pinned before first role closure')
    roles.verify_inputs({'reviewed_files': {EXTERNAL_VALIDATOR: expected}})
    spec = importlib.util.spec_from_file_location('backup_external_clean', EXTERNAL_VALIDATOR)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    validated = module.validate_receipt(receipt_path)
    return {**validated, 'validator_sha256': expected}


def original_roles(directory, complete_digest):
    directory = roles.private(directory, True)
    complete_path = roles.private(directory / 'complete.json')
    roles.need(roles.sha(complete_path) == complete_digest, 'Parent closure receipt changed')
    complete = read(complete_path)
    roles.need(complete.get('version') == 1 and complete.get('full_pair_captured') is False,
               'Actual first-phase role closure required')
    roles.need(not (directory / 'INCOMPLETE.json').exists(), 'Failed parent generation cannot be finalized')
    selected = {}
    for name in (*CONFIG_ROLES, *HEAVY_ROLES):
        path = roles.private(directory / name)
        record = complete['roles'][name]
        roles.need(path.stat().st_size == record['bytes'] and roles.sha(path) == record['sha256'],
                   'Previously closed role changed')
        selected[name] = {'path': str(path), **record}
    # complete.json pins the tar, not its loose sidecars. Bind the loose
    # identities to the unique regular member inside that verified archive.
    identities = roles.private(directory / 'frozen-identities.PRIVATE.json')
    roles.need(identities.stat().st_size <= 16 * 1024 * 1024, 'Oversized frozen identities')
    original = identities.read_bytes()
    found = False
    with tarfile.open(directory / 'controller-config', 'r|*') as archive:
        for number, member in enumerate(archive):
            roles.need(number < 100000, 'Controller archive member limit exceeded')
            if member.name != str(identities).lstrip('/'):
                continue
            roles.need(not found and member.isfile() and member.size == len(original),
                       'Frozen identities archive member is missing, duplicated or unsafe')
            with archive.extractfile(member) as source:
                roles.need(source.read(16 * 1024 * 1024 + 1) == original,
                           'Frozen identities differ from closed archive')
            found = True
    roles.need(found, 'Frozen identities missing from closed archive')
    before = json.loads(original)['config']
    started = read(directory / 'started.json')
    roles.need(started['config_sha256'] == hashlib.sha256(roles.canonical(before)).hexdigest(),
               'Parent configuration does not match closure start')
    return before, complete, selected


def finalize(config, source, complete_digest, output, inherited):
    cold_pair.native_host()
    roles.need(len(inherited) == len(roles.LOCKS), 'Continuous parent operation locks required')
    parent = roles.private(Path(output).parent, True)
    stage = parent / Path(output).name
    roles.need(not stage.exists(), 'Never overwrite a final configuration generation')
    with roles.operator_locks(inherited) as outer_fds, \
         cold_pair.lock_file(WORKER['worker_lock'], cold_pair.WORKER_MARKER, True) as worker_fd, \
         cold_pair.lock_file(str(roles.DB) + '.maintenance.lock', cold_pair.CONTROLLER_MARKER, True) as db_fd:
        roles.CAPTURE_FDS = outer_fds + (worker_fd, db_fd)
        try:
            cold_pair.verify_unit(WORKER)
            before, complete, selected = original_roles(source, complete_digest)
            start = read(START)
            pause_path, clean_path = start['pause_proof'], start['clean_receipt']
            clean = read(clean_path)
            external = external_evidence(before, config, clean_path, clean)
            closure = external['closure'] if external else ()
            validate_transition(before, config, pause_path, clean_path, closure)
            validate_receipts(start, pause_path, clean_path, read(pause_path), clean, external)
            roles.need(roles.sha(roles.private('/var/lib/sandboxd/secrets.key')) == complete['roles']['controller-key']['sha256'],
                       'Controller encryption key changed')
            def observe():
                cold_pair.verify_unit(WORKER)
                roles.observe(config, True)
                cold_pair.validate_pause_receipt(roles.DB, Path(pause_path))
                if external:
                    roles.need(external_evidence(before, config, clean_path, read(clean_path)) == external,
                               'External proof closure changed during finalization')
            observe()
            stage.mkdir(mode=0o700)
            roles.write(stage / 'started.json', {'version': 1, 'at': time.time(),
                        'parent_complete_sha256': complete_digest,
                        'config_sha256': hashlib.sha256(roles.canonical(config)).hexdigest()})
            results = {name: selected[name] for name in HEAVY_ROLES}
            try:
                # Preserve the original immutable image export and frozen
                # container definitions inside the new controller archive.
                image = config['image_archive']
                roles.need(roles.sha(roles.private(image['path'])) == image['sha256'], 'Image archive changed')
                roles.write(stage / 'final-identities.PRIVATE.json', {'config': config,
                            'parent_complete_sha256': complete_digest, 'parent_directory': str(source)})
                for name in CONFIG_ROLES:
                    paths = config['role_paths'][name]
                    if name == 'controller-config':
                        paths = paths + [image['path'], str(Path(source) / 'frozen-identities.PRIVATE.json'),
                                         str(stage / 'final-identities.PRIVATE.json')]
                    record = roles.archive(paths, stage / name, max_bytes=roles.remaining_budget(config, results, stage))
                    results[name] = {'path': str(stage / name), **record}
                    observe()
                # The source receipt and closed heavy files remain immutable;
                # this map selects new configs without rewriting old evidence.
                roles.need(roles.sha(Path(source) / 'complete.json') == complete_digest, 'Parent receipt changed during finalization')
                roles.write(stage / 'complete.json', {'version': 1, 'closed_at': time.time(),
                            'parent_complete_sha256': complete_digest, 'roles': results,
                            'controller-key': complete['roles']['controller-key'],
                            'pause-receipt': {'path': pause_path, 'sha256': roles.sha(pause_path)},
                            'clean-receipt': {'path': clean_path, 'sha256': roles.sha(clean_path)},
                            'full_pair_captured': False, 'application_restore_verified': False})
                fd = os.open(stage, os.O_RDONLY | os.O_DIRECTORY)
                try: os.fsync(fd)
                finally: os.close(fd)
            except BaseException:
                roles.write(stage / 'INCOMPLETE.json', {'failed_at': time.time(), 'accepted': False})
                raise
        finally:
            roles.CAPTURE_FDS = ()
    print(json.dumps({'configuration_roles_finalized': True, 'output': str(stage), 'full_pair_captured': False}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('--closed-roles', required=True)
    parser.add_argument('--closed-complete-sha256', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--inherited-lock-fds', required=True)
    args = parser.parse_args()
    finalize(read(args.config), args.closed_roles, args.closed_complete_sha256, args.output,
             tuple(int(x) for x in args.inherited_lock_fds.split(',')))


if __name__ == '__main__':
    try: main()
    except Exception as error:
        print(type(error).__name__ + ': ' + str(error), file=sys.stderr)
        sys.exit(1)
