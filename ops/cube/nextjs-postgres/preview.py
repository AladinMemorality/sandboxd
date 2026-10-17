#!/usr/bin/env python3
"""Loopback-only preview for the retained operator candidate, including HMR.

Run on worker-01 beside native.py. Forward localhost:3196 over SSH to review.
The only upstream is this journal's port-3000 preview, never the supervisor.
"""
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import selectors
import signal
import socket
import sys
from native import Candidate

candidate = Candidate(sys.argv[1])
candidate.load()
candidate.resume()
stopping = False


def stop(_signal, _frame):
    global stopping
    stopping = True


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)


class Proxy(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *_args):
        pass

    def forward(self):
        if self.headers.get('Host') not in ('localhost:3196', '127.0.0.1:3196') or not self.path.startswith('/') or self.path.startswith('//'):
            self.send_error(403)
            return
        hop = {'host', 'connection', 'transfer-encoding', 'upgrade', 'proxy-authorization', 'proxy-connection',
               'authorization', 'cube-traffic-access-token', 'x-forwarded-host', 'x-forwarded-proto'}
        headers = {key: value for key, value in self.headers.items() if key.lower() not in hop}
        headers.update(candidate.preview)
        # Preserve the browser's localhost origin for Next's development/HMR
        # origin check. Only application writes need the upstream Host origin.
        if self.command == 'POST' and headers.get('Origin') in ('http://localhost:3196', 'http://127.0.0.1:3196'):
            headers['Origin'] = 'http://' + candidate.preview['Host']
        if self.headers.get('Upgrade', '').lower() == 'websocket':
            headers.update({'Connection': 'Upgrade', 'Upgrade': 'websocket'})
            with socket.create_connection(('127.0.0.1', 80), timeout=15) as upstream:
                request = f'GET {self.path} HTTP/1.1\r\n' + ''.join(f'{k}: {v}\r\n' for k, v in headers.items()) + '\r\n'
                upstream.sendall(request.encode('latin1'))
                response = b''
                while b'\r\n\r\n' not in response and len(response) < 65536:
                    chunk = upstream.recv(4096)
                    if not chunk:
                        return
                    response += chunk
                self.connection.sendall(response)
                with selectors.DefaultSelector() as selector:
                    selector.register(upstream, selectors.EVENT_READ, self.connection)
                    selector.register(self.connection, selectors.EVENT_READ, upstream)
                    while not stopping:
                        for key, _ in selector.select(timeout=1):
                            chunk = key.fileobj.recv(65536)
                            if not chunk:
                                return
                            key.data.sendall(chunk)
            self.close_connection = True
            return
        try:
            size = int(self.headers.get('Content-Length', '0'))
        except ValueError:
            self.send_error(400)
            return
        if size < 0 or size > 1024 * 1024 or self.headers.get('Transfer-Encoding'):
            self.send_error(413)
            return
        connection = http.client.HTTPConnection('127.0.0.1', 80, timeout=60)
        try:
            connection.request(self.command, self.path, self.rfile.read(size) if size else None, headers)
            response = connection.getresponse()
            body = response.read(32 * 1024 * 1024)
            self.send_response(response.status)
            for key, value in response.getheaders():
                if key.lower() not in ('connection', 'transfer-encoding', 'content-length'):
                    self.send_header(key, value)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            if self.command != 'HEAD':
                self.wfile.write(body)
        finally:
            connection.close()

    do_GET = do_POST = do_HEAD = do_OPTIONS = forward


server = ThreadingHTTPServer(('127.0.0.1', 3196), Proxy)
server.timeout = 1
print(json.dumps({'sandbox_id': candidate.identity, 'preview': 'http://localhost:3196', 'private': True}), flush=True)
try:
    while not stopping:
        # A guest supervisor restart invalidates the authenticated channel.
        # Production has its controller reconciler; this private preview must
        # also reconnect instead of leaving the app stopped indefinitely.
        if candidate.channel is not None and candidate.channel.poll() is not None:
            candidate.stop_channel()
            candidate.start_channel()
            candidate.ready()
        server.handle_request()
finally:
    server.server_close()
    candidate.pause()
