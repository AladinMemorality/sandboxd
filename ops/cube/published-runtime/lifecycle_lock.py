"""Share CubeMaster's per-guest pause/resume/delete fence with maintenance.

The private config pins the same Redis database as CubeMaster. A failed or
interrupted exec retains its lock for operator review; no uncertain guest
command is declared finished merely because its client disconnected.
"""
import json
import hashlib
import os
import time
import re
import socket
import uuid
import urllib.request

PREFIX = 'cube:v1:master:lock:sandbox:'
UNLOCK = 'if redis.call("GET",KEYS[1])==ARGV[1] then return redis.call("DEL",KEYS[1]) else return 0 end'
# No expiry: a killed updater must not allow a snapshot while its guest exec
# might still run. Normal completion releases by token. The health monitor
# flags retained maintenance locks; recovery requires confirming exec exit.


class Redis:
    def __init__(self, config):
        self.config = config

    def __call__(self, *args):
        with socket.create_connection((self.config['host'], self.config['port']), timeout=5) as sock, sock.makefile('rb') as stream:
            def reply(depth=0):
                if depth > 3: raise RuntimeError('Redis response nesting limit')
                line = stream.readline(65537)
                if len(line)>65536 or not line.endswith(b'\r\n'):
                    raise RuntimeError('invalid Redis response')
                kind, value = line[:1], line[1:-2]
                if kind == b'+': return value.decode()
                if kind == b':': return int(value)
                if kind == b'$':
                    size = int(value)
                    if size == -1: return None
                    if not 0 <= size <= 65536: raise RuntimeError('Redis response size limit')
                    data = stream.read(size+2)
                    if len(data) != size+2 or not data.endswith(b'\r\n'): raise RuntimeError('truncated Redis response')
                    return data[:-2].decode()
                if kind == b'*':
                    size = int(value)
                    if not 0 <= size <= 10000: raise RuntimeError('Redis response count limit')
                    return [reply(depth+1) for _ in range(size)]
                # Never include a Redis error/configuration value in logs.
                raise RuntimeError('lifecycle lock Redis command failed')
            def command(*values):
                parts = [str(v).encode() for v in values]
                sock.sendall(b'*'+str(len(parts)).encode()+b'\r\n'+b''.join(b'$'+str(len(v)).encode()+b'\r\n'+v+b'\r\n' for v in parts))
                return reply()
            command('AUTH', self.config['password'])
            command('SELECT', self.config['database'])
            return command(*args)


def master_running(cid, origin):
    request = urllib.request.Request(origin+'/cube/sandbox/info?sandbox_id='+cid+'&instance_type=cubebox', headers={'X-Caller':'baarcha-maintenance'})
    http = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with http.open(request, timeout=10) as response:
        data = json.load(response)
    rows = data.get('data', [])
    return data.get('ret', {}).get('ret_code') == 200 and len(rows) == 1 and rows[0].get('sandbox_id') == cid and rows[0].get('status') == 1


def guarded_execute(cid, execute, redis, running):
    if not re.fullmatch('[a-f0-9]{32}', cid):
        raise ValueError('invalid runtime identity')
    key, token = PREFIX+cid, 'supervisor-maintenance:'+uuid.uuid4().hex
    if redis('SET', key, token, 'NX') != 'OK':
        return {'runtime_id':cid, 'status':'busy', 'reason':'lifecycle_operation'}
    # An error before dispatch is safe to release. Once dispatched, an exception
    # retains the fence because killing ctr alone does not prove guest exit.
    dispatched = False
    completed = False
    try:
        if not running(cid):
            return {'runtime_id':cid, 'status':'skipped', 'reason':'not_running'}
        dispatched = True
        result = execute()
        completed = True
        return result
    finally:
        if not dispatched or completed:
            if redis('EVAL', UNLOCK, 1, key, token) != 1:
                raise RuntimeError('maintenance lifecycle lock ownership changed')


def observe_retained_locks(redis, path, now=None):
    """Report long-held maintenance fences; never expire or delete them."""
    now = time.time() if now is None else now
    try: previous = json.loads(path.read_text())
    except (FileNotFoundError, ValueError): previous = {}
    tracked, issues, cursor = {}, [], '0'
    for _ in range(1000):
        cursor, keys = redis('SCAN', cursor, 'MATCH', PREFIX+'*', 'COUNT', 100)
        for key in keys:
            token = redis('GET', key)
            if not token or not token.startswith('supervisor-maintenance:'): continue
            digest = hashlib.sha256((key+token).encode()).hexdigest()
            first = previous.get('tracked', {}).get(digest, now)
            if not isinstance(first, (int, float)) or first > now: first = now
            tracked[digest] = first
            if now-first >= 300:
                issues.append({'runtime_id':key[len(PREFIX):], 'first_seen_at':first})
        if cursor == '0': break
    else: raise RuntimeError('lifecycle lock observation limit')
    report = {'checked_at':now, 'tracked':tracked, 'issues':issues}
    temp = path.with_suffix('.tmp')
    with temp.open('w') as handle:
        os.chmod(temp, 0o600)
        json.dump(report, handle); handle.flush(); os.fsync(handle.fileno())
    os.replace(temp, path)
    if issues:
        print('<4>'+json.dumps({'event':'maintenance_recovery_required','issues':issues}), flush=True)
    return report
