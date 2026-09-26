"""Native tests use disposable named services, never the Motion service."""
import http.client
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('natural_stop', Path(__file__).with_name('natural_stop.py'))
n = importlib.util.module_from_spec(spec); spec.loader.exec_module(n)

SOURCE = """
import http from 'node:http';
import fs from 'node:fs/promises';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createRequire} from 'node:module';
const sharp = createRequire('/opt/baarcha/motion-studio/app/package.json')('sharp');
const handler = async (req, res) => {
  await fs.writeFile('started', 'yes');
  await promisify(execFile)(process.execPath, ['-e', 'setTimeout(()=>{},1500)']);
  const bytes = await sharp({create: {width: 512, height: 512, channels: 3, background: '#aaccee'}}).png().toBuffer();
  const f = await fs.open('completed.png', 'wx'); await f.writeFile(bytes); await f.sync(); await f.close();
  res.end('done');
};
if(process.env.STUDIO_WORKER_SOCKET) await new Promise(resolve => http.createServer(handler).listen(process.env.STUDIO_WORKER_SOCKET, resolve));
const server = http.createServer(handler);
server.listen(Number(process.env.PORT), process.env.STUDIO_BIND, () => fs.writeFile('ready.json', JSON.stringify(server.address())));
"""


@unittest.skipUnless(os.geteuid() == 0 and Path('/run/systemd/system').exists() and n.NODE.exists(),
                     'native Linux root, systemd and reviewed Node required')
class NativeStopTests(unittest.TestCase):
    def command(self, args, timeout=30):
        result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
        self.assertEqual(result.returncode, 0, args[0] + ' failed')
        return result.stdout

    def setUp(self):
        self.assertEqual(n.digest(n.NODE), n.NODE_SHA)
        self.assertFalse(self.command(['ss', '-Hltn', 'sport = :9229']).strip())
        self.temp = tempfile.TemporaryDirectory(prefix='cube-motion-stop-fixture-', dir='/root')
        self.root = Path(self.temp.name); (self.root / 'server').mkdir()
        (self.root / 'server/index.mjs').write_text(SOURCE)
        self.unit = 'cube-motion-stop-fixture-' + self.root.name.rsplit('-', 1)[1] + '.service'
        self.unitfile = Path('/run/systemd/system') / self.unit
        self.wanted = Path('/run/systemd/system/multi-user.target.wants') / self.unit
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0)); self.port = sock.getsockname()[1]
        self.unitfile.write_text('[Service]\nType=exec\nRestart=on-failure\nWorkingDirectory=' + str(self.root) +
                                '\nExecStart=' + str(n.NODE) + ' server/index.mjs\nEnvironment=STUDIO_BIND=127.0.0.1\nEnvironment=PORT=' + str(self.port) +
                                '\nEnvironment=STUDIO_WORKER_SOCKET=' + str(self.root / 'worker.sock') + '\nMemoryMax=512M\nCPUQuota=100%\nTasksMax=128\nNoNewPrivileges=yes\nKillMode=control-group\nSendSIGKILL=no\nTimeoutStopSec=infinity\n')
        self.addCleanup(self.cleanup)
        # Keep the disposable unit referenced after exit, just like the enabled
        # production service, so systemd retains its actual exit observation.
        self.wanted.parent.mkdir(exist_ok=True)
        self.wanted.symlink_to(self.unitfile)
        self.command(['systemctl', 'daemon-reload'])
        self.command(['systemctl', 'start', self.unit])
        self.wait_file('ready.json'); self.events = []

    def cleanup(self):
        # Only this independently created disposable service may receive TERM.
        self.command(['systemctl', 'stop', self.unit])
        self.wanted.unlink(); self.unitfile.unlink(); self.command(['systemctl', 'daemon-reload']); self.temp.cleanup()

    def wait_file(self, name):
        until = time.monotonic() + 10
        while not (self.root / name).exists():
            self.assertLess(time.monotonic(), until); time.sleep(.01)

    def service(self):
        fields = 'ActiveState,SubState,MainPID,ControlPID,Restart,Job,InvocationID,ExecMainStartTimestampMonotonic,NRestarts,Result,ExecMainCode,ExecMainStatus'
        raw = self.command(['systemctl', 'show', self.unit, '--property=' + fields])
        return dict(row.split('=', 1) for row in raw.decode().splitlines())

    def invoke(self, uid=0):
        with mock.patch.object(n, 'APP', self.root), mock.patch.object(n, 'TCP', '127.0.0.1:' + str(self.port)), mock.patch.object(n, 'SOCKET', str(self.root / 'worker.sock')):
            return n.stop(service=self.service, command=self.command, fence=lambda: None,
                          event=lambda event, **data: self.events.append((event, data)),
                          stage=self.root, uid=uid, helper_sha256=n.digest(n.HELPER), timeout=10)

    def test_pidfd_systemd_normal_exit_preserves_real_disconnected_sharp_write(self):
        connection = http.client.HTTPConnection('127.0.0.1', self.port)
        connection.request('POST', '/write', headers={'Connection': 'close'})
        self.wait_file('started'); connection.close(); time.sleep(.05)
        self.assertFalse((self.root / 'completed.png').exists())
        result = self.invoke()
        self.assertTrue(result['natural_exit']); self.assertFalse(result['forced'])
        self.assertEqual(result['exit_code'], 0)
        config = json.loads(next(self.root.glob('natural-drain-*.json')).read_text())
        self.assertEqual(config['listeners'], ['127.0.0.1:' + str(self.port), str(self.root / 'worker.sock')])
        self.assertFalse((self.root / 'worker.sock').exists())
        self.assertEqual((self.root / 'completed.png').read_bytes()[1:4], b'PNG')
        self.assertEqual(self.service()['ExecMainCode'], '1')
        self.assertEqual([event for event, _ in self.events], ['natural_stop_intent', 'natural_listener_close_acknowledged', 'natural_worker_exit_observed', 'natural_worker_exited'])

    def test_wrong_service_uid_never_signals_or_opens_inspector(self):
        with mock.patch.object(signal, 'pidfd_send_signal', wraps=signal.pidfd_send_signal) as send:
            with self.assertRaisesRegex(RuntimeError, 'UID changed'):
                self.invoke(uid=1)
            send.assert_not_called()
        self.assertEqual(self.service()['ActiveState'], 'active')
        self.assertFalse(self.command(['ss', '-Hltn', 'sport = :9229']).strip())
        self.assertEqual(self.events, [])

    def test_release_wrapper_accepts_only_its_own_closed_generation(self):
        spec = importlib.util.spec_from_file_location('native_release', Path(__file__).with_name('release.py'))
        release = importlib.util.module_from_spec(spec); spec.loader.exec_module(release)
        r = release.Release({'service_uid': 0}, self.root, {}, b'')
        module = r.natural_module()  # Actual companion hashes and module loading.
        r.service = self.service; r.cmd = self.command; r.fence = lambda: None
        r.projects = lambda: {'projects': []}; r.event = lambda event, **data: self.events.append((event, data))
        r.natural_module = lambda: module
        with mock.patch.object(module, 'APP', self.root), mock.patch.object(module, 'TCP', '127.0.0.1:' + str(self.port)), mock.patch.object(module, 'SOCKET', str(self.root / 'worker.sock')):
            r.natural_completion()
        self.assertEqual(r.closed_identity['ExecMainStatus'], '0')
        count = len(self.events); r.natural_completion()
        self.assertEqual(len(self.events), count)  # No repeated signal/close.


if __name__ == '__main__':
    unittest.main()
