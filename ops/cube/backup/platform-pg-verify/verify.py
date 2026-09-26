#!/usr/bin/env python3
"""Restore a real captured platform dump into an exact disposable network-none PG17."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import resource
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
import select_role_members as selected

IMAGE = 'postgres@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24'
LABEL = 'baarcha.cube.platform-restore'
APP = '01M3CZB4HXT2Y8HP8CEY75PCWY'
need = selected.need


def write(path, value):
    selected.write_receipt(path, value)
    fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try: os.fsync(fd)
    finally: os.close(fd)


def command(args, timeout=15, stdin=None, log=None, check=True):
    if args and args[0] == 'docker':
        args = ['docker', '--host=unix:///var/run/docker.sock', *args[1:]]
    kwargs = {'stdout': subprocess.PIPE, 'stderr': subprocess.PIPE}
    if log is not None:
        kwargs = {'stdout': log, 'stderr': log,
                  'preexec_fn': lambda: resource.setrlimit(resource.RLIMIT_FSIZE, (8 << 20, 8 << 20))}
    p = subprocess.run(args, stdin=stdin, timeout=timeout, **kwargs)
    need(not check or p.returncode == 0, 'Isolated fixture command failed')
    need(log is not None or len(p.stdout) <= 1 << 20, 'Oversized fixture response')
    return p


DIAGNOSTIC_SQL = """BEGIN READ ONLY;
SELECT jsonb_build_object('server_version',version(),
 'server_version_num',current_setting('server_version_num'),
 'server_encoding',current_setting('server_encoding'),
 'date_style',current_setting('DateStyle'),
 'database_locale_provider',d.datlocprovider,'database_collate',d.datcollate,
 'database_ctype',d.datctype,'database_locale',to_jsonb(d)->>'datlocale',
 'database_icu_locale',to_jsonb(d)->>'daticulocale',
 'database_collation_version',d.datcollversion)
FROM pg_database d WHERE d.datname=current_database();
COMMIT;"""


def private_query_result(stage, name, result):
    # Persist the bounded response before status/JSON validation so failed
    # queries and malformed output remain diagnosable without console leakage.
    need(re.fullmatch('[a-z-]+', name), 'Invalid diagnostic name')
    write(stage / (name + '.PRIVATE.json'), {
        'returncode': result.returncode,
        'stdout_bytes': len(result.stdout), 'stderr_bytes': len(result.stderr),
        'stdout_sha256': hashlib.sha256(result.stdout).hexdigest(),
        'stderr_sha256': hashlib.sha256(result.stderr).hexdigest(),
        'stdout': result.stdout[:1 << 20].decode('utf-8', errors='replace'),
        'stderr': result.stderr[:65536].decode('utf-8', errors='replace'),
        'truncated': len(result.stdout) > 1 << 20 or len(result.stderr) > 65536})
    need(result.returncode == 0, 'Diagnostic query failed; retain private result')
    need(len(result.stdout) <= 1 << 20 and len(result.stderr) <= 65536, 'Query diagnostic exceeds bound')
    return json.loads(result.stdout)


def proof_difference(expected, actual):
    if not isinstance(actual, dict): return {'actual_object': False}
    tables = actual.get('tables', {})
    return {'actual_object': True,
            'top_level_keys_equal': set(expected) == set(actual),
            'tables_equal': {name: isinstance(tables, dict) and tables.get(name) == value
                             for name, value in expected['tables'].items()},
            'canary_equal': expected['canary'] == actual.get('canary'),
            'fixture_owners_equal': expected['fixture_owners'] == actual.get('fixture_owners')}


def create_args(name, run):
    return ['docker', 'create', '--pull=never', '--name', name, '--label', LABEL + '=' + run,
            '--network=none', '--userns=host', '--read-only', '--user=70:70', '--cap-drop=ALL',
            '--security-opt=no-new-privileges:true', '--cpus=1', '--memory=512m', '--memory-swap=512m',
            '--pids-limit=128', '--shm-size=32m',
            '--tmpfs', '/var/lib/postgresql/data:rw,noexec,nosuid,nodev,size=384m,uid=70,gid=70,mode=0700',
            '--tmpfs', '/var/run/postgresql:rw,noexec,nosuid,nodev,size=8m,uid=70,gid=70,mode=0775',
            '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=8m,mode=1777',
            '--env', 'PGDATA=/var/lib/postgresql/data', '--env', 'POSTGRES_DB=restoreproof',
            '--env', 'POSTGRES_HOST_AUTH_METHOD=trust', IMAGE,
            'postgres', '-c', 'listen_addresses=', '-c', 'shared_buffers=32MB',
            '-c', 'max_connections=10', '-c', 'work_mem=2MB', '-c', 'maintenance_work_mem=32MB']


def inspect_owned(cid, name, run, image):
    c = json.loads(command(['docker', 'inspect', cid]).stdout)[0]
    need(c['Id'] == cid and c['Name'] == '/' + name and c['Image'] == image
         and c['Config']['Labels'].get(LABEL) == run, 'Container identity changed; do not clean up')
    h = c['HostConfig']
    need(h['NetworkMode'] == 'none' and not h.get('PortBindings') and h['ReadonlyRootfs']
         and h['Memory'] == 512 << 20 and h['MemorySwap'] == 512 << 20
         and h['NanoCpus'] == 1_000_000_000 and h['PidsLimit'] == 128
         and not h.get('Privileged') and h.get('PidMode') != 'host'
         and h.get('CapDrop') == ['ALL'] and 'no-new-privileges:true' in h.get('SecurityOpt', [])
         and not h.get('Binds') and all(m['Type'] == 'tmpfs' for m in c['Mounts']),
         'Fixture isolation changed')
    return c


def validate_proof(expected, actual):
    need(actual == expected, 'Restored source fingerprints differ')
    need(set(actual['tables']) == {'waitlist', 'platform_session', 'published_app', 'upload', 'schema_migrations'},
         'Required source provenance missing')
    for name, record in actual['tables'].items():
        need(type(record['count']) is int and 0 < record['count'] <= 200000
             and re.fullmatch('[0-9a-f]{64}', record['sha256']), 'Invalid table provenance')
    c = actual['canary']
    need(actual['fixture_owners'] == 2 and c['project_id'] == APP and c['owner'] == 103
         and c['visibility'] == 'private' and c['upload_owner'] == 103 and c['upload_project'] == APP
         and c['cover_id'] and c['upload_deleted'] is None
         and re.fullmatch('[0-9a-f]{64}', c['upload_sha256'])
         and 0 < c['upload_bytes'] <= 16 << 20 and c['upload_storage'] in ('s3', 'disk'),
         'Restored private cover/owner provenance differs')


def verify(source, stage, source_sha):
    selected.cold_pair.native_host()
    source = selected.regular(source, 1 << 20)
    need(selected.cold_pair.digest(source) == source_sha, 'Reviewed source receipt changed')
    record = json.loads(source.read_bytes())
    need(record['version'] == 1 and record['kind'] == 'independent-platform-pg-snapshot'
         and record['source_major'] == 17 and record['image'] == IMAGE and record['full_pair'] is False,
         'Wrong source scope')
    query = (Path(__file__).parent / 'provenance.sql').read_bytes()
    need(hashlib.sha256(query).hexdigest() == record['provenance_sql_sha256'], 'Provenance query changed')
    dump = selected.regular(source.parent / 'platform-db', 128 << 20)
    need(dump.stat().st_size == record['dump']['bytes'] and selected.cold_pair.digest(dump) == record['dump']['sha256'],
         'Captured dump changed')
    validate_proof(record['proof'], record['proof'])
    stage = Path(stage).absolute()
    selected.cold_pair.private_directory(stage.parent)
    need(not stage.exists(), 'New private proof stage required')
    image = json.loads(command(['docker', 'image', 'inspect', IMAGE]).stdout)[0]
    need(image['Id'] == 'sha256:' + IMAGE.split('@sha256:')[1] and image['Architecture'] == 'amd64', 'Cached image differs; never pull implicitly')
    need(set(image.get('Config', {}).get('Volumes', {})) == {'/var/lib/postgresql/data'},
         'Image-declared volume contract changed; do not create anonymous volumes')
    run = os.urandom(8).hex()
    name = 'cube-pair-platform-pg-' + run
    stage.mkdir(mode=0o700)
    write(stage / 'intent.json', {'version': 1, 'run': run, 'name': name, 'source_sha256': source_sha, 'created_at': time.time()})
    cid = None
    passed = False
    phase = 'create'
    def mark(value):
        nonlocal phase
        phase = value
        write(stage / ('phase-' + value + '.json'), {'phase': value, 'at': time.time()})
    try:
        mark('create')
        ack = command(create_args(name, run), timeout=30).stdout.decode().strip()
        need(re.fullmatch('[0-9a-f]{64}', ack), 'Create acknowledgement ambiguous; retain intent')
        cid = ack
        write(stage / 'created.json', {'id': cid, 'image': image['Id']})
        inspect_owned(cid, name, run, image['Id'])
        mark('start-readiness')
        command(['docker', 'start', cid], timeout=30)
        deadline = time.monotonic() + 30
        while True:
            p = command(['docker', 'exec', '--user=70:70', cid, 'pg_isready', '-U', 'postgres', '-d', 'restoreproof'], check=False)
            if p.returncode == 0: break
            need(time.monotonic() < deadline, 'PostgreSQL readiness bound exceeded')
            time.sleep(.25)
        inspect_owned(cid, name, run, image['Id'])
        version = command(['docker', 'exec', '--user=70:70', cid, 'psql', '-qAt', '-U', 'postgres', '-d', 'restoreproof', '-c', 'SHOW server_version_num']).stdout.decode().strip()
        need(version.isdigit() and 170000 <= int(version) < 180000, 'PostgreSQL major changed')
        mark('server-diagnostics')
        diagnostics = command(['docker', 'exec', '--user=70:70', cid, 'psql', '-qAt', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'restoreproof', '-c', DIAGNOSTIC_SQL], check=False)
        private_query_result(stage, 'server-locale-version', diagnostics)
        mark('restore')
        log_fd = os.open(stage / 'restore.PRIVATE.log', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with dump.open('rb') as input_file, os.fdopen(log_fd, 'wb') as log:
            command(['docker', 'exec', '-i', '--user=70:70', cid, 'pg_restore', '-U', 'postgres', '--dbname=restoreproof',
                     '--single-transaction', '--exit-on-error', '--clean', '--if-exists', '--no-owner', '--no-privileges'],
                    timeout=300, stdin=input_file, log=log)
            log.flush(); os.fsync(log.fileno())
        mark('provenance-query')
        sql = b"BEGIN READ ONLY; SET LOCAL TIME ZONE 'UTC';\n" + query + b"\nCOMMIT;\n"
        # Temporary regular stdin file avoids loading a dump into process memory.
        fd = os.open(stage / 'query.sql', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as f: f.write(sql); f.flush(); os.fsync(f.fileno())
        with (stage / 'query.sql').open('rb') as f:
            p = command(['docker', 'exec', '-i', '--user=70:70', cid, 'psql', '-qAt', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'restoreproof'], stdin=f, timeout=60, check=False)
        actual = private_query_result(stage, 'actual-provenance', p)
        mark('comparison')
        write(stage / 'comparison.PRIVATE.json', proof_difference(record['proof'], actual))
        validate_proof(record['proof'], actual)
        need(selected.cold_pair.digest(source) == source_sha and selected.cold_pair.digest(dump) == record['dump']['sha256'], 'Source changed during proof')
        passed = True
        mark('validated')
    except Exception as error:
        write(stage / 'failure.json', {'phase': phase, 'exception_type': type(error).__name__, 'at': time.time(), 'verified': False})
        raise
    finally:
        if cid:
            owned = inspect_owned(cid, name, run, image['Id'])
            if owned['State']['Running']: command(['docker', 'stop', '--time=30', cid], timeout=40)
            need(not inspect_owned(cid, name, run, image['Id'])['State']['Running'], 'Owned fixture did not stop')
            command(['docker', 'rm', cid])
            need(command(['docker', 'inspect', cid], check=False).returncode != 0, 'Owned fixture remains')
    need(passed, 'Restore not verified')
    result = {'version': 1, 'actual_platform_pg_restore_verified': True, 'source_sha256': source_sha,
              'dump_sha256': record['dump']['sha256'], 'image': IMAGE, 'tables': record['proof']['tables'],
              'private_owner_upload_session_provenance_verified': True, 'owned_container_removed': True,
              'full_pair_verified': False, 'http_acl_verified': False, 's3_object_restore_verified': False}
    write(stage / 'verified.json', result)
    print(json.dumps({'actual_platform_pg_restore_verified': True, 'owned_container_removed': True, 'full_pair_verified': False, 'http_acl_verified': False}))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--source', required=True)
    p.add_argument('--source-sha256', required=True)
    p.add_argument('--stage', required=True)
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    need(a.execute, 'Explicit reviewed execution required')
    verify(a.source, a.stage, a.source_sha256)


if __name__ == '__main__':
    try: main()
    except Exception: raise SystemExit('Isolated PostgreSQL proof refused; retain private stage. No production database mutation performed.')
