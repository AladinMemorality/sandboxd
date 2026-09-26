"""Close the reviewed Motion listeners, then observe normal Node completion.

Caller must continuously own the release's traffic/writer fences and locks.
Only SIGUSR1 through the exact Linux pidfd is sent. No termination signal,
systemctl stop, request cancellation, process.exit or automatic retry is used.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import select
import signal
import stat
import time

NODE = Path('/opt/baarcha/node22/bin/node')
NODE_SHA = '3517c2df0b2f8cd7f422b4b8450ef81c6889f08eb03e281d6de9079b15e6a327'
NODE_VERSION = 'v22.23.2'
APP = Path('/opt/baarcha/motion-studio/app')
TCP = '172.19.0.1:8332'
SOCKET = '/run/baarcha-motion-studio/worker.sock'
DEBUG_PORT = 9229
HELPER = Path(__file__).with_name('natural_drain.mjs')


def need(ok, message):
    if not ok:
        raise RuntimeError(message)


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def ticks(pid):
    raw = Path('/proc/' + str(pid) + '/stat').read_text()
    return raw[raw.rfind(')') + 2:].split()[19]


def process_identity(properties, uid):
    need(properties.get('ActiveState') == 'active' and properties.get('SubState') == 'running' and
         properties.get('Restart') == 'on-failure' and properties.get('ControlPID') == '0' and
         properties.get('Job', '') in ('', '0'), 'exact running on-failure worker without queued job required')
    pid = int(properties['MainPID']); need(pid > 1, 'live worker PID required')
    start = ticks(pid); root = Path('/proc/' + str(pid))
    need((root / 'exe').resolve(strict=True) == NODE and (root / 'cwd').resolve(strict=True) == APP,
         'Motion Node executable or working directory changed')
    need((root / 'cmdline').read_bytes().split(b'\0')[:-1] == [str(NODE).encode(), b'server/index.mjs'],
         'exact Motion entrypoint required')
    ids = re.search(r'^Uid:\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)', (root / 'status').read_text(), re.M)
    need(ids is not None and all(int(value) == uid for value in ids.groups()), 'worker UID changed')
    env = dict(row.split(b'=', 1) for row in (root / 'environ').read_bytes().split(b'\0') if b'=' in row)
    need(not env.get(b'NODE_OPTIONS') and not env.get(b'STUDIO_WORKER_URL'), 'unreviewed Node options or proxy worker')
    need(env.get(b'STUDIO_BIND', b'').decode() + ':' + env.get(b'PORT', env.get(b'STUDIO_PORT', b'')).decode() == TCP,
         'worker TCP listener configuration changed')
    socket = env.get(b'STUDIO_WORKER_SOCKET', b'').decode()
    need(socket in ('', SOCKET), 'unexpected worker socket')
    need(re.fullmatch(r'[a-f0-9]{32}', properties.get('InvocationID', '')) and
         re.fullmatch(r'[1-9][0-9]*', properties.get('ExecMainStartTimestampMonotonic', '')) and
         properties.get('NRestarts', '').isdigit(), 'service invocation identity missing')
    need(ticks(pid) == start, 'worker process generation changed')
    return {'pid': pid, 'start': start, 'invocation': properties['InvocationID'],
            'service_started': properties['ExecMainStartTimestampMonotonic'],
            'restarts': properties['NRestarts'], 'listeners': [TCP] + ([SOCKET] if socket else [])}


def inspector_owned(pid):
    """Require the only debug listener to be IPv4 loopback and owned by Node."""
    listeners = []
    # Observe the listening socket before collecting process FDs: the inspector
    # thread can create its FD between two /proc reads during normal startup.
    for kind in ('tcp', 'tcp6'):
        for row in Path('/proc/net/' + kind).read_text().splitlines()[1:]:
            fields = row.split(); address, port = fields[1].split(':')
            if fields[3] == '0A' and int(port, 16) == DEBUG_PORT:
                listeners.append((kind, address, fields[9]))
    if not listeners:
        return False
    owned = set()
    for fd in Path('/proc/' + str(pid) + '/fd').iterdir():
        try:
            link = os.readlink(fd)
        except FileNotFoundError:
            continue
        match = re.fullmatch(r'socket:\[(\d+)\]', link)
        if match:
            owned.add(match[1])
    need(len(listeners) == 1 and listeners[0][0:2] == ('tcp', '0100007F') and listeners[0][2] in owned,
         'inspector is not an exclusive worker-owned loopback listener')
    return True


def stop(*, service, command, fence, event, stage, uid, helper_sha256, timeout=240):
    need(os.geteuid() == 0 and hasattr(os, 'pidfd_open') and hasattr(signal, 'pidfd_send_signal'),
         'native Linux root with pidfds required')
    need(0 < timeout <= 300, 'bounded natural drain required')
    for path, expected in ((NODE, NODE_SHA), (HELPER, helper_sha256)):
        need(path.resolve(strict=True) == path and digest(path) == expected, 'reviewed Node/helper hash changed')
        info = path.stat()
        # The installed Node distribution retains its archive UID/GID 1001,
        # including bin/, below the root-owned /opt/baarcha/node22 directory.
        # Accept only that exact existing layout and binary, without chowning a
        # shared runtime as an incidental part of this worker release.
        owner = (1001, 1001, 0o755) if path == NODE else (0, 0, stat.S_IMODE(info.st_mode))
        need(stat.S_ISREG(info.st_mode) and (info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode)) == owner and not info.st_mode & 0o022,
             'untrusted Node/helper file')
        for parent in path.parents:
            st = parent.stat()
            need(stat.S_ISDIR(st.st_mode) and not st.st_mode & 0o022 and
                 (st.st_uid, st.st_gid) == ((1001, 1001) if path == NODE and parent == NODE.parent else (0, 0)),
                 'unreviewed Node/helper ancestor')
    fence()
    before = service(); identity = process_identity(before, uid); pid = identity['pid']
    need(not command(['ss', '-Hltn', 'sport = :' + str(DEBUG_PORT)]).strip(), 'existing debugger listener needs separate review')
    config = {'version': 1, 'pid': pid, 'uid': uid, 'inspector_port': DEBUG_PORT,
              'node_version': NODE_VERSION, 'exec_path': str(NODE), 'cwd': str(APP),
              'argv': [str(NODE), str(APP / 'server/index.mjs')], 'listeners': identity['listeners']}
    # /proc cmdline retains the relative launch argument; Node's process.argv
    # resolves its entry script to the absolute path used by the CDP assertion.
    config_path = Path(stage) / ('natural-drain-' + identity['invocation'] + '.json')
    fd = os.open(config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(config, f); f.flush(); os.fsync(f.fileno())
    descriptor = os.pidfd_open(pid, 0)
    try:
        need(process_identity(service(), uid) == identity, 'worker generation changed before debugger signal')
        fence(); event('natural_stop_intent', process=identity)
        signal.pidfd_send_signal(descriptor, signal.SIGUSR1)
        deadline = time.monotonic() + 5
        while not inspector_owned(pid):
            need(time.monotonic() < deadline, 'worker inspector did not open; no repeated signal')
            time.sleep(0.05)
        need(process_identity(service(), uid) == identity, 'worker changed after debugger startup')
        fence()
        result = json.loads(command([str(NODE), str(HELPER), str(config_path), '--execute'], timeout=20))
        need(result == {'version': 1, 'pid': pid, 'listeners': identity['listeners'],
                        'close_requested': True, 'process_exit_requested': False, 'drained': False},
             'listener close was not acknowledged; retain fence')
        event('natural_listener_close_acknowledged', pid=pid)
        poll = select.poll(); poll.register(descriptor, select.POLLIN)
        deadline = time.monotonic() + timeout
        while not poll.poll(250):
            need(time.monotonic() < deadline, 'natural completion pending; no termination fallback')
            # The pidfd continues to identify the original process even if its
            # numeric PID is reused. Inspect the reaped unit once signalled;
            # sampling /proc between poll calls would race a legitimate exit.
        # pidfd is signalled before systemd has necessarily reaped the process.
        deadline = time.monotonic() + 10
        while True:
            after = service()
            if after.get('MainPID') == '0' and after.get('ActiveState') == 'inactive':
                break
            need(time.monotonic() < deadline, 'systemd has not confirmed natural worker exit')
            time.sleep(0.05)
        observation = {key: after.get(key) for key in ('Result', 'ExecMainCode', 'ExecMainStatus',
                       'NRestarts', 'ExecMainStartTimestampMonotonic')}
        event('natural_worker_exit_observed', **observation)
        need(after.get('Result') == 'success' and after.get('ExecMainCode') == '1' and
             after.get('ExecMainStatus') == '0' and after.get('NRestarts') == identity['restarts'] and
             after.get('ExecMainStartTimestampMonotonic') == identity['service_started'],
             'exact worker did not complete with normal exit zero: ' + json.dumps(observation, sort_keys=True))
        need(not Path('/proc/' + str(pid)).exists(), 'old Node PID still exists')
        need(not command(['ss', '-Hltn', 'sport = :' + str(DEBUG_PORT)]).strip(), 'inspector listener remains')
        fence(); event('natural_worker_exited', process=identity, forced=False, exit_code=0)
        return {'version': 1, 'natural_exit': True, 'pid': pid, 'start': identity['start'],
                'invocation': identity['invocation'], 'exit_code': 0, 'forced': False}
    finally:
        os.close(descriptor)
