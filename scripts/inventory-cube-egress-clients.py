#!/usr/bin/env python3
"""Read-only, bounded client-capability inventory. No tenant code is executed.

Outputs allowlisted capability names and file counts only: no source, command,
URL, environment value, credential, or package version is emitted. A clean scan
is evidence for review, not proof of absence of dynamically loaded clients.
"""
import argparse
import datetime
import json
import os
import re
import sqlite3
import stat

RAW = {'pg', 'postgres', 'mysql', 'mysql2', 'mongodb', 'mongoose', 'redis', 'ioredis',
       '@prisma/client', 'knex', 'sequelize', 'mssql', 'tedious', 'amqplib', 'kafkajs'}
HTTP = {'@supabase/supabase-js', '@neondatabase/serverless', '@libsql/client',
        '@upstash/redis', 'axios', 'node-fetch', 'undici', 'ws', 'socket.io-client'}
LOCAL = {'better-sqlite3', 'sqlite3', 'sql.js', 'bun:sqlite'}
SKIP = {'node_modules', '.git', '.next', '.venv', 'venv', 'dist', 'build', 'vendor', '.cache', '.runtimed'}
PATTERNS = {
    'raw_tcp_module': rb'''(?:from\s*|require\s*\(\s*|import\s*\(\s*)["'](?:node:)?(?:net|tls)["']''',
    'database_url_reference': rb'\b(?:DATABASE_URL|POSTGRES_URL|REDIS_URL|MONGODB_URI|MYSQL_URL)\b',
    'raw_database_scheme': rb'(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://',
    'native_fetch': rb'\bfetch\s*\(',
    'custom_http_agent': rb'\b(?:setGlobalDispatcher|ProxyAgent|httpsAgent|httpAgent|dispatcher)\b',
    'websocket': rb'\b(?:WebSocket|socket\.io)\b',
    'bridge': rb'\bBRIDGE_URL\b|/api/bridge',
    'java_network_client': rb'\b(?:java\.net\.(?:Socket|DatagramSocket)|HttpURLConnection|java\.net\.http|org\.apache\.http|okhttp3)\b',
    'python_db_client': rb'\b(?:psycopg2?|asyncpg|pymysql|pymongo|sqlalchemy|mysqlclient)\b',
}


def open_root(path):
    if not os.path.isabs(path) or any(p in ('.', '..') for p in path.split('/')):
        raise ValueError('unsafe inventory root')
    fd = os.open('/', os.O_RDONLY | os.O_DIRECTORY)
    try:
        for name in filter(None, path.split('/')):
            nxt = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = nxt
        return fd
    except BaseException:
        os.close(fd)
        raise


def read_file(directory, name, maximum):
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > maximum:
            raise ValueError('file is not a bounded regular file')
        body = os.read(fd, maximum + 1)
        if len(body) > maximum:
            raise ValueError('file exceeded limit while reading')
        return body
    finally:
        os.close(fd)


def inspect(root, private_details=False, reviewed_exclusions=()):
    result = {'raw_tcp_dependencies': [], 'http_dependencies': [], 'local_database_dependencies': [],
              'source_capabilities': [], 'files_read': 0, 'bytes_read': 0, 'manifests_read': 0,
              'incomplete_reasons': []}
    deps, capabilities, issues = set(), set(), set()
    entries = 0
    details = []
    for path in reviewed_exclusions:
        if path.startswith("/") or any(p in ("", ".", "..") for p in path.split("/")):
            raise ValueError("invalid reviewed exclusion")
    fd = open_root(root)
    device = os.fstat(fd).st_dev

    def walk(directory, depth, relative=""):
        nonlocal entries
        with os.scandir(directory) as iterator:
            for entry in iterator:
                entries += 1
                path = relative + "/" + entry.name if relative else entry.name
                if entries > 2000 or result['files_read'] >= 256 or result['bytes_read'] >= 4 * 1024 * 1024:
                    issues.add('scan_limit')
                    if private_details: details.append({'path': path, 'reason': 'scan_limit'})
                    return
                name = entry.name
                if name in SKIP or name.startswith('.') or path in reviewed_exclusions:
                    continue
                info = entry.stat(follow_symlinks=False)
                if stat.S_ISLNK(info.st_mode):
                    issues.add('symlink_not_followed')
                    if private_details: details.append({'path': path, 'reason': 'symlink_not_followed'})
                    continue
                if stat.S_ISDIR(info.st_mode):
                    if depth >= 4:
                        issues.add('depth_limit')
                        if private_details: details.append({'path': path, 'reason': 'depth_limit'})
                        continue
                    child = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
                    try:
                        if os.fstat(child).st_dev != device:
                            issues.add('mount_not_followed')
                        else:
                            walk(child, depth + 1, path)
                    finally:
                        os.close(child)
                    continue
                package = name == 'package.json'
                source = name.endswith(('.js', '.jsx', '.ts', '.tsx', '.mjs', '.cjs', '.py', '.java'))
                python = name in ('requirements.txt', 'pyproject.toml')
                if not (package or source or python):
                    continue
                try:
                    body = read_file(directory, name, min(256 * 1024, 4 * 1024 * 1024 - result['bytes_read']))
                    result['files_read'] += 1
                    result['bytes_read'] += len(body)
                    if package:
                        data = json.loads(body)
                        for section in ('dependencies', 'devDependencies', 'optionalDependencies'):
                            value = data.get(section, {})
                            if isinstance(value, dict):
                                deps.update(value.keys())
                        result['manifests_read'] += 1
                    else:
                        for capability, pattern in PATTERNS.items():
                            if re.search(pattern, body):
                                capabilities.add(capability)
                                if private_details and capability in {"raw_tcp_module", "custom_http_agent", "python_db_client"}: details.append({"path": path, "reason": capability})
                except (OSError, ValueError, TypeError, AttributeError):
                    issues.add('unreadable_or_oversized_source')
    try:
        walk(fd, 0)
    finally:
        os.close(fd)
    result['raw_tcp_dependencies'] = sorted(deps & RAW)
    result['http_dependencies'] = sorted(deps & HTTP)
    result['local_database_dependencies'] = sorted(deps & LOCAL)
    result['source_capabilities'] = sorted(capabilities)
    result['incomplete_reasons'] = sorted(issues)
    if private_details: result['private_path_details'] = details
    result['requires_raw_client_review'] = bool(deps & RAW or capabilities & {'raw_tcp_module', 'database_url_reference', 'raw_database_scheme', 'python_db_client'})
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--database', default='/var/lib/sandboxd/state/sandboxd.db')
    parser.add_argument('--workspaces', default='/var/lib/sandboxd/workspaces')
    parser.add_argument('--private-details', action='store_true', help='Include private relative paths for manual classification; never publish these details')
    args = parser.parse_args()
    db = sqlite3.connect('file:' + args.database + '?mode=ro', uri=True)
    rows = db.execute('SELECT id FROM sandbox ORDER BY id').fetchall()
    if len(rows) > 1000:
        raise ValueError('fleet exceeds bounded inventory limit')
    projects = []
    for (identity,) in rows:
        if not re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}', identity):
            raise ValueError('noncanonical sandbox identity')
        item = {'sandbox_id': identity}
        try:
            item.update(inspect(os.path.join(args.workspaces, identity, 'workspace', 'app'), args.private_details))
        except (OSError, ValueError):
            item['incomplete_reasons'] = ['workspace_unavailable']
        projects.append(item)
    print(json.dumps({'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                      'method': 'read-only bounded static capability scan; no URLs, secrets or source emitted',
                      'sandbox_count': len(projects), 'raw_client_review_count': sum(x.get('requires_raw_client_review', False) for x in projects),
                      'incomplete_count': sum(bool(x['incomplete_reasons']) for x in projects),
                      'projects': projects}, sort_keys=True, indent=2))

if __name__ == '__main__':
    main()
