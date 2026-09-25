#!/usr/bin/env python3
"""No-extraction conversion for the fixed synthetic PostgreSQL recovery scope.

Whole-home tar is confidential recovery input, never publication/remix input.
Successful conversion still requires the existing Go import validators and live
PostgreSQL/latest-marker verification in a fenced clean replacement.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import posixpath
import stat
import subprocess
import sys
import zipfile

from archive_validate import InvalidArchive, Limits, member_path, validate

PRESERVE = ('.bashrc', '.bash_logout', '.profile', '.cache', '.baarcha-postgres', '.cube-crash-fixture')
MANIFEST = {'version': 2, 'entries': [
    {'path': 'workspace/app', 'disposition': 'separate'},
    {'path': '.runtimed', 'disposition': 'separate'},
    *({'path': path, 'disposition': 'preserve'} for path in PRESERVE)]}


def scope(name, entry):
    if name == 'workspace' or name == 'workspace/app':
        if entry['kind'] != 'dir':
            raise InvalidArchive('workspace_scope_is_not_directory')
        return None
    if name.startswith('workspace/app/'):
        return 'app', name[len('workspace/app/'):]
    if name == '.runtimed' or name.startswith('.runtimed/'):
        return None  # Separate supervisor identity; never copy old credentials.
    if any(name == root or name.startswith(root + '/') for root in PRESERVE):
        return 'home', name
    raise InvalidArchive('unreviewed_home_scope_requires_import_plan')


def regular_target(name, index):
    seen = set()
    while index[name]['kind'] == 'hardlink':
        if name in seen:
            raise InvalidArchive('cyclic_hardlink')
        seen.add(name)
        name = member_path(index[name]['target'])
    if index[name]['kind'] != 'file':
        raise InvalidArchive('hardlink_not_regular')
    return name, index[name]


def conversion_plan(index):
    result, expanded, counts = [], {'app': 0, 'home': 0}, {'app': 0, 'home': 0}
    for root in PRESERVE:
        if root not in index:
            raise InvalidArchive('required_preserved_home_root_missing')
    if 'workspace/app' not in index:
        raise InvalidArchive('required_workspace_root_missing')
    for name, entry in sorted(index.items()):
        selected = scope(name, entry)
        if selected is None:
            continue
        group, destination = selected
        if entry['kind'] == 'hardlink':
            target_name, content = regular_target(name, index)
            if scope(target_name, content) is None:
                raise InvalidArchive('hardlink_into_separate_runtime_identity')
            if content['mode'] != entry['mode']:
                raise InvalidArchive('ambiguous_hardlink_mode')
        else:
            content = entry
        size = len(entry['target'].encode()) if entry['kind'] == 'symlink' else content['size']
        if size > 1 << 30 or expanded[group] + size > 8 << 30:
            raise InvalidArchive('expanded_zip_limit_including_hardlinks')
        counts[group] += 1
        if counts[group] > (200000 if group == 'app' else 500000):
            raise InvalidArchive('zip_entry_limit')
        expanded[group] += size
        result.append((group, destination, entry, content))
    return result, expanded


def source_stamp(stream):
    info = os.fstat(stream.fileno())
    return info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns


def convert(source, output, expected_marker, *, go_validator=None):
    if expected_marker is None:
        raise InvalidArchive('owned_latest_marker_required')
    fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > Limits().archive_bytes:
            raise InvalidArchive('bounded_regular_archive_required')
        before = source_stamp(stream)
        report, index = validate(stream, require_postgres=True, expected_marker=expected_marker, return_index=True)
        plan, expanded = conversion_plan(index)
        output = Path(output)
        output.mkdir(mode=0o700)  # Exclusive: no partial/previous evidence overwrite.
        paths = {name: output / (name + '.zip') for name in ('app', 'home')}
        handles = {name: path.open('xb') for name, path in paths.items()}
        for handle in handles.values(): os.chmod(handle.name, 0o600)
        writers = {name: zipfile.ZipFile(handle, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=1)
                   for name, handle in handles.items()}
        try:
            for group, destination, entry, content in plan:
                kind = 'file' if entry['kind'] == 'hardlink' else entry['kind']
                name = destination + ('/' if kind == 'dir' else '')
                item = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
                item.create_system = 3
                item.compress_type = zipfile.ZIP_DEFLATED
                type_bits = stat.S_IFDIR if kind == 'dir' else stat.S_IFLNK if kind == 'symlink' else stat.S_IFREG
                item.external_attr = ((type_bits | entry['mode']) << 16) | (0x10 if kind == 'dir' else 0)
                size = len(entry['target'].encode()) if kind == 'symlink' else content['size']
                item.file_size = size
                with writers[group].open(item, 'w') as dest:
                    if kind == 'symlink':
                        dest.write(entry['target'].encode())
                    elif kind == 'file':
                        stream.seek(content['offset'])
                        remaining = size
                        while remaining:
                            chunk = stream.read(min(65536, remaining))
                            if not chunk:
                                raise InvalidArchive('source_changed_or_truncated')
                            dest.write(chunk)
                            remaining -= len(chunk)
                            if handles[group].tell() > 4 << 30:
                                raise InvalidArchive('compressed_zip_limit')
            for writer in writers.values(): writer.close()
            for handle in handles.values():
                handle.flush()
                if handle.tell() > 4 << 30: raise InvalidArchive('compressed_zip_limit')
                os.fsync(handle.fileno())
        finally:
            for writer in writers.values(): writer.close()
            for handle in handles.values(): handle.close()
        stream.seek(0)
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        if source_stamp(stream) != before or digest != report['archive_sha256']:
            raise InvalidArchive('source_changed_during_conversion')
    manifest = output / 'home-manifest.json'
    with manifest.open('x') as handle:
        os.chmod(manifest, 0o600)
        json.dump(MANIFEST, handle, indent=2)
        handle.flush(); os.fsync(handle.fileno())
    result = dict(source_archive_sha256=digest, flattened_hardlinks=sum(e['kind'] == 'hardlink' for _, _, e, _ in plan),
                  expanded_zip_bytes=expanded, excluded_runtime_identity=True,
                  postmaster_pid_preserved='.baarcha-postgres/data/postmaster.pid' in index,
                  extracted_host_paths=False, go_import_contract_validated=False,
                  replacement_verified=False, production_accepted=False, task_history_converted=False,
                  expected_marker_sha256=hashlib.sha256(json.dumps(expected_marker, sort_keys=True).encode()).hexdigest())
    if go_validator:
        checked = subprocess.run([str(go_validator), str(paths['app']), str(paths['home']), str(manifest)],
                                 capture_output=True, text=True, timeout=180, check=False)
        if checked.returncode or len(checked.stdout) > 65536:
            raise InvalidArchive('existing_go_import_contract_rejected')
        try:
            outcome = json.loads(checked.stdout)
        except ValueError:
            raise InvalidArchive('invalid_go_validator_result') from None
        if outcome.get('valid') is not True:
            raise InvalidArchive('existing_go_import_contract_rejected')
        result['go_import_contract_validated'] = True
        result['go_validation'] = outcome
    for name, path in paths.items():
        with path.open('rb') as stream:
            result[name + '_sha256'] = hashlib.file_digest(stream, 'sha256').hexdigest()
    with (output / 'conversion.json').open('x') as handle:
        os.chmod(handle.name, 0o600)
        json.dump(result, handle, indent=2)
        handle.flush(); os.fsync(handle.fileno())
    directory = os.open(output, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try: os.fsync(directory)
    finally: os.close(directory)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('archive')
    parser.add_argument('new_output_directory')
    parser.add_argument('--expected-marker', required=True)
    parser.add_argument('--go-validator', required=True)
    args = parser.parse_args()
    try:
        fd = os.open(args.expected_marker, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'rb') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise InvalidArchive('private_operator_marker_required')
            raw = stream.read(4097)
            if len(raw) > 4096: raise InvalidArchive('operator_marker_limit')
        result = convert(args.archive, args.new_output_directory, json.loads(raw), go_validator=args.go_validator)
        print(json.dumps(result, sort_keys=True))
    except (InvalidArchive, OSError, ValueError, subprocess.SubprocessError) as error:
        code = str(error) if isinstance(error, InvalidArchive) else 'conversion_failed'
        print(json.dumps({'converted': False, 'error': code, 'replacement_verified': False}))
        return 1
    return 0

if __name__ == '__main__': sys.exit(main())
