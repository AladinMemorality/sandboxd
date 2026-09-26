#!/usr/bin/env python3
"""Validate an online rsync cache and reconcile it only under a caller's fence.

No service lifecycle actions. A prepared cache is not a backup. The only public
CLI operations are online preparation and a read-only delta estimate.
"""
import argparse
import contextlib
import errno
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import subprocess
import time

MAX_ENTRIES = 5_000_000
MAX_BYTES = 64 * 1024**3
MAX_DELTA_BYTES = 512 * 1024**2
MAX_DELTA_FILES = 50_000


def need(value, message):
    if not value:
        raise RuntimeError(message)


def digest(path):
    h = hashlib.sha256()
    with open(path, 'rb') as stream:
        for chunk in iter(lambda: stream.read(1024**2), b''):
            h.update(chunk)
    return h.hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'))


def publish(path, value):
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(canonical(value) + '\n')
        stream.flush()
        os.fsync(stream.fileno())


def generation(path):
    s = path.lstat()
    row = dict(path=str(path), device=s.st_dev, inode=s.st_ino, mode=s.st_mode,
               uid=s.st_uid, gid=s.st_gid, nlink=s.st_nlink, bytes=s.st_size,
               mtime_ns=s.st_mtime_ns, ctime_ns=s.st_ctime_ns)
    if stat.S_ISLNK(s.st_mode):
        row['target'] = os.readlink(path)
    return row


def normalized(roots):
    values = sorted({str(Path(root)) for root in roots})
    need(values and len(values) == len(roots), 'Exact nonempty unique roots required')
    for root in map(Path, values):
        need(root.is_absolute() and root != Path('/') and '..' not in root.parts,
             'Unsafe source root')
        need(not any(Path(other) in root.parents for other in values), 'Nested roots')
    return values


def contained(path, roots):
    p = Path(path)
    return p.is_absolute() and '..' not in p.parts and str(p) == path and any(
        p == Path(root) or Path(root) in p.parents for root in roots)


def records(roots):
    count = total = 0
    inodes = set()
    for root in roots:
        root = Path(root)
        need(root.resolve() == root and not root.is_symlink(), 'Source root changed')
        pending = [root]
        while pending:
            path = pending.pop()
            row = generation(path)
            count += 1
            need(count <= MAX_ENTRIES, 'Source entry ceiling exceeded')
            if stat.S_ISREG(row['mode']):
                key = row['device'], row['inode']
                if key not in inodes:
                    inodes.add(key)
                    total += row['bytes']
                need(total <= MAX_BYTES, 'Source byte ceiling exceeded')
            yield row
            if stat.S_ISDIR(row['mode']):
                with os.scandir(path) as entries:
                    pending.extend(Path(e.path) for e in entries)


def database(path):
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    os.close(fd)
    db = sqlite3.connect(path, uri=True)
    db.execute('PRAGMA journal_mode=DELETE')
    db.execute('PRAGMA synchronous=FULL')
    db.execute('PRAGMA temp_store=FILE')
    return db


def table(db, name):
    db.execute(f'CREATE TABLE {name}(path BLOB PRIMARY KEY, row TEXT NOT NULL) WITHOUT ROWID')


def add(db, name, row, roots):
    need(contained(row['path'], roots), 'Manifest escaped reviewed roots')
    db.execute(f'INSERT INTO {name} VALUES(?,?)', (os.fsencode(row['path']), canonical(row)))


def load_scan(db, name, path, expected, roots):
    need(digest(path) == expected['sha256'], 'Online scan changed')
    table(db, name)
    with open(path) as stream:
        for count, line in enumerate(stream, 1):
            need(count <= MAX_ENTRIES, 'Manifest entry ceiling exceeded')
            add(db, name, json.loads(line), roots)
    db.commit()


def metadata_equal(source, cached):
    fields = ['mode', 'uid', 'gid', 'mtime_ns']
    if stat.S_ISREG(source['mode']):
        fields.append('bytes')
    if stat.S_ISLNK(source['mode']):
        fields.append('target')
    return all(source.get(k) == cached.get(k) for k in fields)


def file_digest(path, expected):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as stream:
        s = os.fstat(stream.fileno())
        need(stat.S_ISREG(s.st_mode) and (s.st_dev, s.st_ino) ==
             (expected['device'], expected['inode']), 'Cache file changed before hashing')
        h = hashlib.sha256()
        for data in iter(lambda: stream.read(1024**2), b''):
            h.update(data)
    need(generation(path) == expected, 'Cache file changed while hashing')
    return h.hexdigest()


def private_directory(path):
    path = Path(path)
    s = path.lstat()
    need(path.is_absolute() and path.resolve() == path and stat.S_ISDIR(s.st_mode)
         and s.st_uid == os.geteuid() and stat.S_IMODE(s.st_mode) == 0o700,
         'Private canonical cache directory required')
    return path


def prepare(cache):
    cache = private_directory(cache)
    scope = json.loads((cache / 'scope.PRIVATE.json').read_text())
    result = json.loads((cache / 'candidate-only.json').read_text())
    need(result['online_precopy_completed'] and result['rsync_exit'] in (0, 24)
         and not result['backup_accepted'], 'Completed online candidate required')
    roots = normalized(scope['roots'])
    tree = private_directory(cache / 'tree')
    need(scope['destination'] == str(tree), 'Cache destination changed')
    # Preparation runs only after its dedicated copy unit has exited. No live
    # source content is hashed: rsync verified transferred content, while source
    # generations on both sides of that transfer establish which paths are reusable.
    with contextlib.closing(database(cache / 'prepared.sqlite')) as db:
        load_scan(db, 'before', cache / 'before.jsonl', result['before'], roots)
        load_scan(db, 'after', cache / 'after.jsonl', result['after'], roots)
        db.execute('CREATE TABLE cached(path BLOB PRIMARY KEY, row TEXT NOT NULL, sha256 TEXT) WITHOUT ROWID')
        hashes = {}
        for row in records([str(tree / root.lstrip('/')) for root in roots]):
            p = Path(row['path'])
            value = None
            if stat.S_ISREG(row['mode']):
                key = row['device'], row['inode'], row['ctime_ns'], row['mtime_ns'], row['bytes']
                value = hashes.get(key)
                if value is None:
                    value = file_digest(p, row)
                    # Most paths are single-link files; only keep shared hashes.
                    if row['nlink'] > 1:
                        hashes[key] = value
            need(stat.S_ISREG(row['mode']) or stat.S_ISDIR(row['mode']) or stat.S_ISLNK(row['mode']),
                 'Unexpected special file in private cache')
            source = '/' + str(p.relative_to(tree))
            db.execute('INSERT INTO cached VALUES(?,?,?)', (os.fsencode(source), canonical(row), value))
        db.commit()
        counts = {name: db.execute(f'SELECT count(*) FROM {name}').fetchone()[0]
                  for name in ('before', 'after', 'cached')}
    value = dict(version=1, at=time.time(), roots=roots, tree=str(tree), counts=counts,
                 database_sha256=digest(cache / 'prepared.sqlite'),
                 scope_sha256=digest(cache / 'scope.PRIVATE.json'), backup_accepted=False)
    publish(cache / 'prepared.json', value)
    return value


def open_prepared(cache, expected_sha, roots, output):
    cache = private_directory(cache)
    need(not (cache / 'consumed.json').exists(), 'Prepared cache was already consumed')
    need(digest(cache / 'prepared.json') == expected_sha, 'Prepared cache receipt changed')
    receipt = json.loads((cache / 'prepared.json').read_text())
    need(receipt['version'] == 1 and receipt['roots'] == normalized(roots)
         and receipt['tree'] == str(cache / 'tree') and not receipt['backup_accepted'],
         'Prepared cache scope differs')
    need(digest(cache / 'scope.PRIVATE.json') == receipt['scope_sha256'] and
         digest(cache / 'prepared.sqlite') == receipt['database_sha256'], 'Prepared cache evidence changed')
    private_directory(cache / 'tree')
    output = Path(output)
    private_directory(output.parent)
    output.mkdir(mode=0o700)
    db = database(output / 'delta.sqlite')
    db.execute('ATTACH DATABASE ? AS prepared', ((cache / 'prepared.sqlite').as_uri() + '?mode=ro',))
    return db, receipt


def parent_uncertain(path, bad_directories):
    return any(str(p) in bad_directories for p in Path(path).parents)


def safe_cached_generation(tree, name):
    """Never follow a cached symlink while checking or discarding private files."""
    parts = Path(name).parts[1:]
    fd = os.open(tree, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        for part in parts[:-1]:
            try:
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            except OSError as error:
                if error.errno in (errno.ENOENT, errno.ENOTDIR, errno.ELOOP):
                    return None
                raise
            os.close(fd)
            fd = child
        try:
            s = os.stat(parts[-1], dir_fd=fd, follow_symlinks=False)
        except FileNotFoundError:
            return None
        row = dict(path=str(Path(tree) / name.lstrip('/')), device=s.st_dev, inode=s.st_ino,
                   mode=s.st_mode, uid=s.st_uid, gid=s.st_gid, nlink=s.st_nlink,
                   bytes=s.st_size, mtime_ns=s.st_mtime_ns, ctime_ns=s.st_ctime_ns)
        if stat.S_ISLNK(s.st_mode):
            row['target'] = os.readlink(parts[-1], dir_fd=fd)
        return row
    finally:
        os.close(fd)


def analyze(db, receipt, allowed_sockets=(), online=False):
    roots, tree = receipt['roots'], Path(receipt['tree'])
    table(db, 'frozen')
    allowed = {v['path']: v for v in allowed_sockets}
    seen = set()
    for row in records(roots):
        if not (stat.S_ISREG(row['mode']) or stat.S_ISDIR(row['mode']) or stat.S_ISLNK(row['mode'])):
            expected = allowed.get(row['path'])
            need(stat.S_ISSOCK(row['mode']) and (online or (expected and expected['inode'] == row['inode']
                 and expected['device'] == row['device'] and expected.get('reason'))), 'Unreviewed frozen special file')
            seen.add(row['path'])
        add(db, 'frozen', row, roots)
    need(online or seen == set(allowed), 'Reviewed ephemeral socket set changed')
    db.commit()
    # An online directory rename can hide a whole subtree during rsync without
    # changing child inodes. Such descendants cannot reuse the transferred copy.
    bad_dirs = set()
    for before, after in db.execute('SELECT b.row,a.row FROM prepared.before b JOIN prepared.after a USING(path) WHERE b.row<>a.row'):
        first, last = json.loads(before), json.loads(after)
        if any(stat.S_ISDIR(v.get('mode', 0)) for v in (first, last)):
            bad_dirs.add(last['path'])
    for after, in db.execute('SELECT a.row FROM prepared.after a LEFT JOIN prepared.before b USING(path) WHERE b.path IS NULL'):
        row = json.loads(after)
        if stat.S_ISDIR(row.get('mode', 0)):
            bad_dirs.add(row['path'])
    db.execute('CREATE TABLE dirty(path BLOB PRIMARY KEY, bytes INTEGER) WITHOUT ROWID')
    query = '''SELECT f.path,f.row,b.row,a.row,c.row,c.sha256 FROM frozen f
               LEFT JOIN prepared.before b USING(path) LEFT JOIN prepared.after a USING(path)
               LEFT JOIN prepared.cached c USING(path)'''
    count = size = 0
    for path, frozen, before, after, cached, checksum in db.execute(query):
        row = json.loads(frozen)
        if not stat.S_ISREG(row['mode']):
            continue
        old = json.loads(cached) if cached else None
        reusable = (before == after == frozen and old is not None and checksum is not None
                    and not parent_uncertain(row['path'], bad_dirs)
                    and metadata_equal(row, old)
                    and safe_cached_generation(tree, row['path']) == old)
        if not reusable:
            db.execute('INSERT INTO dirty VALUES(?,?)', (path, row['bytes']))
            count += 1
            size += row['bytes']
    db.commit()
    return dict(dirty_files=count, dirty_bytes=size, source_entries=db.execute('SELECT count(*) FROM frozen').fetchone()[0],
                within_budget=count <= MAX_DELTA_FILES and size <= MAX_DELTA_BYTES,
                source_sockets=len(seen), backup_accepted=False)


def discard_dirty(tree, name):
    parts = Path(name).parts[1:]
    fd = os.open(tree, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        for part in parts[:-1]:
            try:
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            except OSError as error:
                if error.errno in (errno.ENOENT, errno.ENOTDIR, errno.ELOOP):
                    return
                raise
            os.close(fd)
            fd = child
        try:
            s = os.stat(parts[-1], dir_fd=fd, follow_symlinks=False)
        except FileNotFoundError:
            return
        if not stat.S_ISDIR(s.st_mode):
            os.unlink(parts[-1], dir_fd=fd)
    finally:
        os.close(fd)


def rsync_args(tree, listing, allowed_sockets=()):
    exclusions = []
    for socket in allowed_sockets:
        name = socket['path']
        # Keep rsync's pattern language out of reviewed exclusions. Unusual
        # socket names require an explicit follow-up, never a broader wildcard.
        need(re.fullmatch(r'/[A-Za-z0-9_./-]+', name) and '..' not in Path(name).parts,
             'Socket path needs literal exclusion review')
        exclusions.append('--exclude=' + name)
    return ['/usr/bin/rsync', '--archive', '--recursive', '--hard-links', '--acls', '--xattrs',
            '--numeric-ids', '--relative', '--no-implied-dirs', '--sparse', '--protect-args', '--from0',
            '--files-from=' + str(listing), '--modify-window=-1', '--delete-before',
            '--no-devices', '--no-specials', *exclusions, '/', str(tree) + '/']


def invoke(args, log, inherited=()):
    with open(log, 'xb') as output:
        os.chmod(log, 0o600)
        result = subprocess.run(args, stdout=output, stderr=subprocess.STDOUT,
                                timeout=180, pass_fds=tuple(inherited))
        output.flush()
        os.fsync(output.fileno())
    need(result.returncode == 0 and Path(log).stat().st_size == 0, 'Cache reconciliation failed or warned')


def seal(cache, expected_sha, roots, output, fence, allowed_sockets=(), inherited=()):
    """Called only by capture_roles after its existing actual writer-fence proof."""
    need(callable(fence), 'Live writer-fence verifier required')
    fence()
    db, receipt = open_prepared(cache, expected_sha, roots, output)
    output = Path(output)
    with contextlib.closing(db):
        report = analyze(db, receipt, allowed_sockets)
        publish(output / 'delta.json', report)
        need(report['within_budget'], 'Delta exceeds reviewed maintenance budget; prepare another online copy')
        fence()
        tree = Path(receipt['tree'])
        # Implied ancestors such as /tmp are outside the reviewed roots and can
        # legitimately change online. Do not compare or restore their metadata.
        # --no-implied-dirs may follow existing ancestors, so explicitly require
        # their canonical directory identities before invoking it.
        for root in receipt['roots']:
            parent = tree / str(Path(root).parent).lstrip('/')
            need(parent.resolve() == parent and parent.is_dir(), 'Unsafe cache ancestor')
        for socket in allowed_sockets:
            need(safe_cached_generation(tree, socket['path']) is None,
                 'Reviewed omitted socket has stale cached data; prepare a new cache')
        publish(Path(cache) / 'consumed.json', {'output': str(output), 'at': time.time(), 'backup_accepted': False})
        for path, in db.execute('SELECT path FROM dirty'):
            discard_dirty(tree, os.fsdecode(path))
        listing = output / 'roots.nul'
        with open(listing, 'xb') as stream:
            os.chmod(listing, 0o600)
            stream.write(b'\0'.join(os.fsencode(root.lstrip('/')) for root in receipt['roots']) + b'\0')
        args = rsync_args(tree, listing, allowed_sockets)
        invoke(args, output / 'sync.PRIVATE.log', inherited)
        # Detect missing/extra paths, metadata, ACL/xattr and hardlink differences.
        # Dirty regular files were removed before rsync, so equal size/mtime can
        # never cause their content transfer to be skipped.
        invoke(args[:-2] + ['--dry-run', '--itemize-changes'] + args[-2:], output / 'verify.PRIVATE.log', inherited)
        for socket in allowed_sockets:
            need(safe_cached_generation(tree, socket['path']) is None, 'Omitted socket unexpectedly present in cache')
        table(db, 'after_seal')
        for row in records(receipt['roots']):
            add(db, 'after_seal', row, receipt['roots'])
        db.commit()
        need(db.execute('SELECT count(*) FROM (SELECT * FROM frozen EXCEPT SELECT * FROM after_seal)').fetchone()[0] == 0
             and db.execute('SELECT count(*) FROM (SELECT * FROM after_seal EXCEPT SELECT * FROM frozen)').fetchone()[0] == 0,
             'Source changed during frozen cache reconciliation')
        fence()
        value = dict(version=1, at=time.time(), tree=str(tree), roots=receipt['roots'], delta=report,
                     prepared_sha256=expected_sha, frozen_sources_verified=True, backup_accepted=False)
        publish(output / 'sealed.json', value)
        return value


def estimate(cache, expected_sha, roots, output, allowed_sockets=()):
    db, receipt = open_prepared(cache, expected_sha, roots, output)
    with contextlib.closing(db):
        value = analyze(db, receipt, allowed_sockets, online=True)
        value['online_estimate_only'] = True
        publish(Path(output) / 'estimate.json', value)
        return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['prepare', 'estimate'])
    parser.add_argument('--cache', required=True)
    parser.add_argument('--prepared-sha256')
    parser.add_argument('--output')
    args = parser.parse_args()
    if args.action == 'prepare':
        print(canonical(prepare(args.cache)))
    else:
        need(args.prepared_sha256 and args.output, 'Pinned prepared receipt and new output required')
        roots = json.loads((Path(args.cache) / 'prepared.json').read_text())['roots']
        print(canonical(estimate(args.cache, args.prepared_sha256, roots, args.output)))


if __name__ == '__main__':
    main()
