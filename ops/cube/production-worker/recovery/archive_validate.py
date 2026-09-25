#!/usr/bin/env python3
"""Validate an uncompressed merged-home tar without extracting or following links.

This is NOT a runtimed app/home-v2 import authorization or PostgreSQL recovery
check. The tar remains confidential same-owner recovery material.
"""
import argparse
from dataclasses import dataclass
import hashlib
import io
import json
import os
import posixpath
import re
import stat
import sys
import tarfile


class InvalidArchive(ValueError):
    """Fixed diagnostic codes never contain tenant names or file contents."""


@dataclass(frozen=True)
class Limits:
    archive_bytes: int = 20 << 30
    payload_bytes: int = 16 << 30
    member_bytes: int = 2 << 30
    entries: int = 500000
    path_bytes: int = 64 << 20
    metadata_bytes: int = 32 << 20
    metadata_record_bytes: int = 64 << 10


class Reader:
    def __init__(self, stream, limit):
        self.stream, self.limit, self.count = stream, limit, 0
        self.digest = hashlib.sha256()

    def read(self, count):
        if count < 0 or count > self.limit - self.count:
            raise InvalidArchive('archive_size_limit')
        result = self.stream.read(count)
        self.count += len(result)
        self.digest.update(result)
        return result

    def exact(self, count):
        result = self.read(count)
        if len(result) != count:
            raise InvalidArchive('truncated_archive')
        return result


def member_path(name, *, root=False, literal_paths=()):
    if not isinstance(name, str) or '\0' in name or len(name.encode('utf-8')) > 4096:
        raise InvalidArchive('invalid_member_path')
    if name.startswith('./'):
        name = name[2:]
    if name in ('', '.'):
        if root:
            return ''
        raise InvalidArchive('empty_member_path')
    if name.startswith('/') or ('\\' in name and name not in literal_paths):
        raise InvalidArchive('absolute_or_unreviewed_literal_path')
    if name.endswith('/'):
        name = name[:-1]
    if len(name.split('/')) > 128 or any(part in ('', '.', '..') for part in name.split('/')):
        raise InvalidArchive('noncanonical_member_path')
    return name


def internal_symlink(name, target):
    if not target or '\0' in target or '\\' in target or len(target.encode('utf-8')) > 4096:
        return None
    if target.startswith('/'):
        if target == '/home/sandbox':
            return ''
        if not target.startswith('/home/sandbox/'):
            return None
        resolved = posixpath.normpath(target)
        if not resolved.startswith('/home/sandbox/'):
            return None
        return resolved[len('/home/sandbox/'):]
    resolved = posixpath.normpath(posixpath.join(posixpath.dirname(name), target))
    return None if resolved == '..' or resolved.startswith('../') else ('' if resolved == '.' else resolved)


def pax_fields(payload):
    result = {}
    while payload:
        space = payload.find(b' ')
        if space < 1 or space > 8 or not payload[:space].isdigit():
            raise InvalidArchive('invalid_pax_length')
        length = int(payload[:space])
        if length <= space + 2 or length > len(payload) or payload[length-1:length] != b'\n':
            raise InvalidArchive('invalid_pax_record')
        record, payload = payload[space+1:length-1], payload[length:]
        key, separator, value = record.partition(b'=')
        if not separator:
            raise InvalidArchive('invalid_pax_key')
        try:
            key, value = key.decode('utf-8'), value.decode('utf-8')
        except UnicodeError:
            raise InvalidArchive('invalid_metadata_encoding') from None
        if key not in {'path', 'linkpath', 'size', 'uid', 'gid', 'mtime', 'atime', 'ctime', 'uname', 'gname'}:
            raise InvalidArchive('unsupported_pax_sparse_xattr_or_extension')
        if key in result:
            raise InvalidArchive('duplicate_pax_key')
        if key in {'size', 'uid', 'gid'} and (not re.fullmatch(r'[0-9]{1,20}', value)):
            raise InvalidArchive('invalid_pax_integer')
        if key in {'mtime', 'atime', 'ctime'} and not re.fullmatch(r'-?[0-9]{1,20}(\.[0-9]{1,20})?', value):
            raise InvalidArchive('invalid_pax_time')
        result[key] = value
    return result


def reviewed_plan(value):
    if value is None:
        return {}, set(), None, None
    if not isinstance(value, dict) or value.get('version') != 1 or not re.fullmatch(r'[a-f0-9]{64}', value.get('archive_sha256', '')) or not re.fullmatch(r'[a-f0-9]{64}', value.get('destination_image_sha256', '')):
        raise InvalidArchive('invalid_reviewed_plan_binding')
    literals = value.get('literal_paths', [])
    links = value.get('links', [])
    if not isinstance(literals, list) or len(literals) > 128 or not isinstance(links, list) or len(links) > 128:
        raise InvalidArchive('reviewed_plan_limit')
    literal_set = set()
    for name in literals:
        canonical = member_path(name, literal_paths=(name,))
        if canonical != name or canonical in literal_set:
            raise InvalidArchive('invalid_reviewed_literal_path')
        literal_set.add(name)
    mapping = {}
    for item in links:
        if not isinstance(item, dict):
            raise InvalidArchive('invalid_reviewed_link')
        name, target = item.get('path'), item.get('target')
        if member_path(name, literal_paths=literal_set) != name or name in mapping or not isinstance(target, str) or not target or '\0' in target or len(target.encode()) > 4096:
            raise InvalidArchive('invalid_reviewed_link')
        mapping[name] = target
    return mapping, literal_set, value['archive_sha256'], value['destination_image_sha256']


def validate(stream, *, limits=Limits(), uid=1000, gid=1000, require_postgres=False,
             expected_marker=None, link_plan=None, return_index=False):
    if expected_marker is not None and (not isinstance(expected_marker, dict) or set(expected_marker) != {'fixture', 'phase', 'nonce'} or expected_marker.get('phase') != 'latest' or any(not re.fullmatch(r'[a-f0-9]{32}', expected_marker.get(key, '')) for key in ('fixture', 'nonce'))):
        raise InvalidArchive('invalid_latest_marker_expectation')
    links, literals, bound_hash, image_hash = reviewed_plan(link_plan)
    reader = Reader(stream, limits.archive_bytes)
    members, pending, captures = {}, {}, {}
    index = {} if return_index else None
    payload_total = path_total = metadata_total = records = 0
    seen_root = False
    used_external = set()
    while True:
        block = reader.exact(512)
        if block == bytes(512):
            if reader.exact(512) != bytes(512) or pending:
                raise InvalidArchive('invalid_tar_termination')
            while True:
                # Read one byte beyond the remaining physical budget only via
                # explicit EOF probing, never allocate from a header size.
                remaining = reader.limit - reader.count
                if remaining == 0:
                    if stream.read(1):
                        raise InvalidArchive('archive_size_limit')
                    break
                tail = reader.read(min(65536, remaining))
                if not tail:
                    break
                if any(tail):
                    raise InvalidArchive('trailing_data_or_concatenated_archive')
            break
        records += 1
        if records > limits.entries * 4 + 1024:
            raise InvalidArchive('header_count_limit')
        try:
            header = tarfile.TarInfo.frombuf(block, encoding='utf-8', errors='strict')
        except (tarfile.TarError, UnicodeError, ValueError):
            raise InvalidArchive('invalid_tar_header') from None
        if header.size < 0:
            raise InvalidArchive('negative_member_size')
        if header.type in (tarfile.XHDTYPE, tarfile.GNUTYPE_LONGNAME, tarfile.GNUTYPE_LONGLINK):
            if header.size > limits.metadata_record_bytes or metadata_total + header.size > limits.metadata_bytes:
                raise InvalidArchive('metadata_size_limit')
            metadata_total += header.size
            data = reader.exact(header.size)
            if any(reader.exact((-header.size) % 512)):
                raise InvalidArchive('nonzero_padding')
            if header.type == tarfile.XHDTYPE:
                fields = pax_fields(data)
            else:
                if not data.endswith(b'\0') or b'\0' in data[:-1]:
                    raise InvalidArchive('invalid_gnu_long_name')
                try:
                    fields = {'path' if header.type == tarfile.GNUTYPE_LONGNAME else 'linkpath': data[:-1].decode('utf-8')}
                except UnicodeError:
                    raise InvalidArchive('invalid_metadata_encoding') from None
            if pending.keys() & fields.keys():
                raise InvalidArchive('ambiguous_extended_header')
            pending.update(fields)
            continue
        if header.type not in (tarfile.REGTYPE, tarfile.AREGTYPE, tarfile.DIRTYPE, tarfile.SYMTYPE, tarfile.LNKTYPE):
            raise InvalidArchive('special_node_or_unsupported_tar_type')
        size = int(pending.get('size', header.size))
        owner, group = int(pending.get('uid', header.uid)), int(pending.get('gid', header.gid))
        if size > limits.member_bytes or header.size > limits.member_bytes or payload_total + size > limits.payload_bytes:
            raise InvalidArchive('payload_size_limit')
        if owner != uid or group != gid:
            raise InvalidArchive('unexpected_numeric_ownership')
        # The canonical runtime archive contract retains permission bits only.
        # Refuse sticky directories instead of silently changing their semantics.
        if header.mode & ~0o777:
            raise InvalidArchive('privilege_or_unsupported_mode_bits')
        directory = header.type == tarfile.DIRTYPE
        name = member_path(pending.get('path', header.name), root=directory, literal_paths=literals)
        target = pending.get('linkpath', header.linkname)
        pending = {}
        if not name:
            if seen_root or not directory or size:
                raise InvalidArchive('duplicate_or_invalid_root')
            seen_root = True
            continue
        if name in members or len(members) >= limits.entries:
            raise InvalidArchive('duplicate_member_or_entry_limit')
        path_total += len(name.encode()) + len(target.encode())
        if path_total > limits.path_bytes:
            raise InvalidArchive('aggregate_path_memory_limit')
        kind = 'file' if header.type in (tarfile.REGTYPE, tarfile.AREGTYPE) else 'dir' if directory else 'symlink' if header.type == tarfile.SYMTYPE else 'hardlink'
        if kind != 'file' and size:
            raise InvalidArchive('nonfile_payload')
        if kind not in ('symlink', 'hardlink') and target:
            raise InvalidArchive('nonlink_target')
        resolved = None
        if kind == 'symlink':
            resolved = internal_symlink(name, target)
            if resolved is None:
                if links.get(name) != target:
                    raise InvalidArchive('external_symlink_requires_exact_review')
                used_external.add(name)
        if kind == 'hardlink':
            resolved = member_path(target, literal_paths=literals)
        members[name] = (kind, size, resolved)
        if index is not None:
            index[name] = {'kind': kind, 'size': size, 'offset': reader.count, 'mode': header.mode & 0o777, 'target': target}
        payload_total += size
        capture = name in ('.baarcha-postgres/data/PG_VERSION', 'workspace/app/crash-fixture-data/marker.json', '.cube-crash-fixture/marker.json')
        if capture and size > 4096:
            raise InvalidArchive('required_evidence_file_oversized')
        collected = bytearray()
        remaining = size
        while remaining:
            piece = reader.exact(min(65536, remaining))
            if capture:
                collected.extend(piece)
            remaining -= len(piece)
        if capture:
            captures[name] = bytes(collected)
        if any(reader.exact((-size) % 512)):
            raise InvalidArchive('nonzero_padding')
    # Order-independent ancestor checking: a later symlink must not convert a
    # previously accepted path into an extraction escape.
    for name, (kind, _, target) in members.items():
        parent = posixpath.dirname(name)
        while parent:
            if parent in members and members[parent][0] != 'dir':
                raise InvalidArchive('non_directory_ancestor')
            parent = posixpath.dirname(parent)
        if kind == 'hardlink':
            seen = {name}
            while True:
                if len(seen) > 128:
                    raise InvalidArchive('hardlink_chain_limit')
                if target in seen or target not in members:
                    raise InvalidArchive('cyclic_or_missing_hardlink_target')
                seen.add(target)
                target_kind, _, next_target = members[target]
                if target_kind == 'file':
                    break
                if target_kind != 'hardlink':
                    raise InvalidArchive('hardlink_target_not_regular')
                target = next_target
        if kind == 'symlink' and target is not None:
            candidate, followed = target, set()
            for _ in range(128):
                if candidate in followed:
                    raise InvalidArchive('cyclic_symlink_chain')
                followed.add(candidate)
                parts = candidate.split('/') if candidate else []
                changed = False
                for position in range(len(parts)):
                    prefix = '/'.join(parts[:position+1])
                    entry = members.get(prefix)
                    if entry and entry[0] == 'symlink':
                        suffix = parts[position+1:]
                        if entry[2] is None:
                            if suffix:
                                raise InvalidArchive('external_symlink_ancestor')
                            # Exact terminal external link was independently
                            # reviewed and bound to this archive; never follow it.
                            changed = False
                            break
                        candidate = posixpath.normpath('/'.join([entry[2], *suffix]))
                        changed = True
                        break
                    if entry and entry[0] in ('file', 'hardlink') and position != len(parts)-1:
                        raise InvalidArchive('non_directory_link_target_ancestor')
                if not changed:
                    break
            else:
                raise InvalidArchive('symlink_chain_limit')
    digest = reader.digest.hexdigest()
    if bound_hash is not None and digest != bound_hash:
        raise InvalidArchive('reviewed_plan_archive_hash_mismatch')
    if require_postgres:
        if captures.get('.baarcha-postgres/data/PG_VERSION', b'').strip() != b'18':
            raise InvalidArchive('postgres_version_missing_or_unsupported')
        control = members.get('.baarcha-postgres/data/global/pg_control')
        wal = [v for k, v in members.items() if re.fullmatch(r'\.baarcha-postgres/data/pg_wal/[A-F0-9]{24}', k)]
        if not control or control[0] != 'file' or control[1] < 512 or not any(v[0] == 'file' and v[1] > 0 for v in wal):
            raise InvalidArchive('postgres_control_or_wal_missing')
    if expected_marker is not None:
        for name in ('workspace/app/crash-fixture-data/marker.json', '.cube-crash-fixture/marker.json'):
            try:
                actual = json.loads(captures[name])
            except (KeyError, ValueError):
                raise InvalidArchive('latest_marker_missing_or_invalid') from None
            if actual != expected_marker:
                raise InvalidArchive('latest_marker_mismatch')
    result = {'valid_merged_home_tar': True, 'archive_sha256': digest, 'archive_bytes': reader.count,
            'entries': len(members), 'payload_bytes': payload_total, 'postgres_required_layout_present': require_postgres,
            'latest_files_match': expected_marker is not None, 'reviewed_external_links': len(used_external),
            'destination_image_sha256': image_hash, 'runtimed_import_authorized': False,
            'postgres_crash_recovery_proven': False, 'extracted': False}
    return (result, index) if return_index else result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('archive')
    parser.add_argument('--require-postgres', action='store_true')
    parser.add_argument('--expected-marker')
    parser.add_argument('--reviewed-link-plan')
    args = parser.parse_args()
    def private_json(name):
        if name is None:
            return None
        fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'rb') as f:
            info = os.fstat(f.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise InvalidArchive('operator_input_not_private')
            data = f.read(65537)
        if len(data) > 65536:
            raise InvalidArchive('operator_input_limit')
        return json.loads(data)
    try:
        marker = private_json(args.expected_marker)
        plan = private_json(args.reviewed_link_plan)
        fd = os.open(args.archive, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'rb') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_size > Limits().archive_bytes:
                raise InvalidArchive('archive_not_bounded_regular_file')
            result = validate(stream, require_postgres=args.require_postgres, expected_marker=marker, link_plan=plan)
        print(json.dumps(result, sort_keys=True))
    except (InvalidArchive, OSError, ValueError, TypeError) as error:
        code = str(error) if isinstance(error, InvalidArchive) else 'invalid_archive_or_operator_input'
        print(json.dumps({'valid_merged_home_tar': False, 'error': code, 'extracted': False}))
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
