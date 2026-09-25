#!/usr/bin/env python3
"""Ordinary local sockets only. No Cube, Docker, namespaces or BPF operations."""
import os
import pathlib
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import time
import unittest

ROOT = pathlib.Path(__file__).resolve().parent
SOCAT = os.environ.get('SOCAT_BINARY') or shutil.which('socat')


def read_all(sock):
    chunks = []
    while True:
        part = sock.recv(16384)
        if not part:
            return b''.join(chunks)
        chunks.append(part)
        if sum(map(len, chunks)) > 2**20:
            raise AssertionError('fixture response bound exceeded')


class Fixture:
    def __init__(self, family, address, handler):
        self.sock = socket.socket(family)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(address)
        self.address = self.sock.getsockname()
        self.sock.listen(8)
        self.sock.settimeout(.1)
        self.stopped = threading.Event()
        self.handler = handler
        self.errors = []
        self.children = []
        self.thread = threading.Thread(target=self.run)
        self.thread.start()

    def run(self):
        while not self.stopped.is_set():
            try:
                conn, _ = self.sock.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            t = threading.Thread(target=self.handle, args=(conn,))
            self.children.append(t)
            t.start()

    def handle(self, conn):
        with conn:
            conn.settimeout(5)
            try:
                self.handler(conn)
            except (ConnectionResetError, BrokenPipeError):
                pass
            except Exception as error:
                self.errors.append(error)

    def close(self):
        self.stopped.set()
        self.sock.close()
        self.thread.join(2)
        for t in self.children:
            t.join(6)
            if t.is_alive():
                raise AssertionError('fixture handler leaked')


@unittest.skipUnless(SOCAT, 'actual socat required; this is not an acceptance pass')
class SidecarTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='cube-relay-', dir='/tmp')
        self.path = str(pathlib.Path(self.tmp.name) / 'upstream.sock')
        s = socket.socket()
        s.bind(('127.0.0.1', 0))
        self.port = s.getsockname()[1]
        s.close()
        self.children = []
        self.fixtures = []

    def tearDown(self):
        for child in self.children:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGTERM)
            child.wait(5)
            child.stderr.close()
        for fixture in self.fixtures:
            fixture.close()
            self.assertFalse(fixture.errors, fixture.errors)
        self.tmp.cleanup()

    def start(self, handler):
        self.fixtures.append(Fixture(socket.AF_UNIX, self.path, handler))
        self.launch()

    def launch(self):
        # Exact production flags; only fixture listener/UDS addresses differ.
        child = subprocess.Popen([SOCAT, '-t', '30', '-T', '300',
            f'TCP4-LISTEN:{self.port},bind=127.0.0.1,reuseaddr,fork,max-children=256,backlog=32',
            f'UNIX-CONNECT:{self.path},connect-timeout=5'],
            start_new_session=True, stderr=subprocess.PIPE)
        self.children.append(child)
        time.sleep(.1)
        self.assertIsNone(child.poll())

    def connect(self):
        return socket.create_connection(('127.0.0.1', self.port), timeout=5)

    def test_http_bytes_headers_and_half_close(self):
        request = b'GET /healthz HTTP/1.1\r\nHost: 3031-fixture.cube.test\r\nX-API-Key: synthetic\r\nAuthorization: Bearer synthetic\r\n\r\n'
        response = b'HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nready'
        observed = []
        def handler(conn):
            observed.append(read_all(conn))
            time.sleep(.8)  # Exceeds socat's dangerous default 0.5 s EOF delay.
            conn.sendall(response)
        self.start(handler)
        with self.connect() as client:
            client.sendall(request)
            client.shutdown(socket.SHUT_WR)
            self.assertEqual(read_all(client), response)
        self.assertEqual(observed, [request])

    def test_bidirectional_upgrade_stream(self):
        def handler(conn):
            headers = b''
            while b'\r\n\r\n' not in headers:
                headers += conn.recv(1024)
            self.assertIn(b'Connection: Upgrade', headers)
            conn.sendall(b'HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n')
            while True:
                data = conn.recv(4096)
                if not data:
                    return
                conn.sendall(data)
        self.start(handler)
        with self.connect() as client:
            client.sendall(b'GET /egress/channel HTTP/1.1\r\nHost: fixture\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n')
            response = b''
            while b'\r\n\r\n' not in response:
                response += client.recv(1024)
            self.assertTrue(response.startswith(b'HTTP/1.1 101'))
            for payload in [b'\x81\x04ping', b'\x00\xff\x01control', b'fresh-stream']:
                client.sendall(payload)
                actual = b''
                while len(actual) < len(payload):
                    actual += client.recv(len(payload)-len(actual))
                self.assertEqual(actual, payload)

    def test_reconnect_after_upstream_disappears(self):
        def handler(conn):
            conn.sendall(b'first')
        self.start(handler)
        with self.connect() as client:
            self.assertEqual(read_all(client), b'first')
        self.fixtures.pop().close()
        os.unlink(self.path)
        with self.connect() as client:
            self.assertEqual(read_all(client), b'')
        self.fixtures.append(Fixture(socket.AF_UNIX, self.path, lambda c: c.sendall(b'new')))
        with self.connect() as client:
            self.assertEqual(read_all(client), b'new')
        self.assertIsNone(self.children[0].poll())


    def test_complete_two_leg_persistent_transport(self):
        request = b'GET / HTTP/1.1\r\nHost: 3000-fixture.cube.test\r\nAuthorization: Bearer synthetic\r\n\r\n'
        response = b'HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok'
        def upstream(conn):
            self.assertEqual(read_all(conn), request)
            time.sleep(.8)
            conn.sendall(response)
        tcp = Fixture(socket.AF_INET, ('127.0.0.1', 0), upstream)
        self.fixtures.append(tcp)
        # Exact persistent host socat flags, with only synthetic addresses.
        host = subprocess.Popen([SOCAT, '-t', '30', '-T', '300',
            f'UNIX-LISTEN:{self.path},unlink-early,mode=0660,fork,max-children=256,backlog=32',
            f'TCP4:127.0.0.1:{tcp.address[1]},connect-timeout=5'],
            start_new_session=True, stderr=subprocess.PIPE)
        self.children.append(host)
        time.sleep(.1)
        self.assertIsNone(host.poll())
        self.launch()
        with self.connect() as client:
            client.sendall(request)
            client.shutdown(socket.SHUT_WR)
            self.assertEqual(read_all(client), response)


class WiringTests(unittest.TestCase):
    def test_fixed_addresses_and_no_payload_logging(self):
        relay = (ROOT/'relay.sh').read_text()
        self.assertIn('bind=127.0.0.1', relay)
        self.assertIn('max-children=256', relay)
        self.assertIn('UNIX-CONNECT:$socket,connect-timeout=5', relay)
        for argument in ['', 'evil,EXEC:sh', 'api extra']:
            run = subprocess.run(['sh', str(ROOT/'relay.sh'), *argument.split()], capture_output=True)
            self.assertEqual(run.returncode, 64)
        for name, port in [('api', 20300), ('proxy', 20080)]:
            service = (ROOT/'systemd'/f'cube-management-{name}.service').read_text()
            self.assertIn(f'TCP4:127.0.0.1:{port},connect-timeout=5', service)
            self.assertIn('User=cube-management-relay', service)
            self.assertIn('CapabilityBoundingSet=\n', service)
            self.assertIn('RuntimeDirectoryMode=0750', service)
            self.assertIn('mode=0660,fork,max-children=256', service)
            self.assertIn('IPAddressDeny=any', service)
        compose = (ROOT/'compose.override.yml').read_text()
        self.assertNotIn('ports:', compose)
        self.assertIn('network_mode: service:sandboxd', compose)
        self.assertIn('read_only: true', compose)




@unittest.skipUnless(os.geteuid() == 0 and os.uname().sysname == 'Linux', 'root Linux needed for actual DAC identity fixture')
class SocketPermissionsTests(unittest.TestCase):
    def test_nonroot_access_requires_exact_socket_group(self):
        with tempfile.TemporaryDirectory(prefix='cube-dac-', dir='/tmp') as directory:
            os.chown(directory, 0, 65534)
            os.chmod(directory, 0o750)
            path = str(pathlib.Path(directory)/'api.sock')
            fixture = Fixture(socket.AF_UNIX, path, lambda c: c.sendall(b'owned'))
            os.chown(path, 0, 65534)
            os.chmod(path, 0o660)
            script = 'import os,socket,sys; os.setgroups([]); os.setgid(int(sys.argv[2])); os.setuid(65532); s=socket.socket(socket.AF_UNIX); s.settimeout(2); s.connect(sys.argv[1]); assert s.recv(5)==b"owned"'
            try:
                allowed = subprocess.run(['python3', '-c', script, path, '65534'], capture_output=True, timeout=5)
                self.assertEqual(allowed.returncode, 0, allowed.stderr)
                denied = subprocess.run(['python3', '-c', script, path, '65532'], capture_output=True, timeout=5)
                self.assertNotEqual(denied.returncode, 0)
                self.assertIn(b'PermissionError', denied.stderr)
            finally:
                fixture.close()
            self.assertFalse(fixture.errors, fixture.errors)


if __name__ == '__main__':
    unittest.main()
