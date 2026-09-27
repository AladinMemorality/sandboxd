#!/usr/bin/env python3
"""Loopback immutable artifact cache; CubeMaster authorizes every download."""
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import stat
from urllib.parse import parse_qs, urlsplit

ROOT = Path('/data/cube-fleet-artifact-cache')


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass  # URLs contain Cube artifact credentials.

    def do_GET(self):
        connection = None
        try:
            uri = urlsplit(self.path)
            query = parse_qs(uri.query)
            assert uri.path == '/cube/template/artifact/download' and not uri.scheme and not uri.netloc
            assert set(query) == {'artifact_id', 'token'} and all(len(v) == 1 for v in query.values())
            artifact = query['artifact_id'][0]
            assert re.fullmatch(r'rfs-[a-f0-9-]+', artifact)
            assert len(query['token'][0]) <= 256
            folder = ROOT / artifact
            ready = json.loads((folder/'verified.json').read_text())
            fd = os.open(folder/'rootfs.ext4', os.O_RDONLY | os.O_NOFOLLOW)
            with os.fdopen(fd, 'rb') as source:
                info = os.fstat(source.fileno())
                assert stat.S_ISREG(info.st_mode) and info.st_uid == 0
                assert [info.st_ino, info.st_size, info.st_mtime_ns] == ready['generation']
                connection = http.client.HTTPConnection('10.254.240.1', 18089, timeout=10)
                headers = {'Range':'bytes=0-0'}
                if self.headers.get('X-Cube-Artifact-Token'):
                    headers['X-Cube-Artifact-Token'] = self.headers['X-Cube-Artifact-Token']
                connection.request('GET', self.path, headers=headers)
                response = connection.getresponse()
                # A one-byte authorized range establishes that the canonical
                # control plane still serves this exact immutable artifact.
                assert response.status == 206
                assert response.getheader('Content-Range') == 'bytes 0-0/'+str(info.st_size)
                assert len(response.read(2)) == 1
                connection.close(); connection = None
                self.send_response(200)
                self.send_header('Content-Type', 'application/octet-stream')
                self.send_header('Content-Length', str(info.st_size))
                self.end_headers()
                while chunk := source.read(1024*1024): self.wfile.write(chunk)
        except (BrokenPipeError, ConnectionResetError):
            pass
        except Exception:
            self.send_error(503, 'Verified template cache unavailable')
        finally:
            if connection: connection.close()


if __name__ == '__main__':
    assert os.geteuid() == 0
    ThreadingHTTPServer(('127.0.0.1', 18089), Handler).serve_forever()
