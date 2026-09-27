import http.client
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('artifact_cache', Path(__file__).with_name('artifact-cache-server.py'))
cache = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cache)
HTTPConnection = http.client.HTTPConnection


@unittest.skipUnless(os.geteuid() == 0, 'root-owned cache contract requires Linux operator test environment')
class CacheTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.folder = self.root / 'rfs-abcd-1234'
        self.folder.mkdir()
        self.file = self.folder / 'rootfs.ext4'
        self.data = b'verified template'
        self.file.write_bytes(self.data)
        s = self.file.stat()
        (self.folder/'verified.json').write_text(json.dumps({'generation':[s.st_ino,s.st_size,s.st_mtime_ns]}))
        self.root_patch = patch.object(cache, 'ROOT', self.root)
        self.root_patch.start()
        self.server = cache.ThreadingHTTPServer(('127.0.0.1', 0), cache.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown(); self.server.server_close(); self.thread.join()
        self.root_patch.stop(); self.tmp.cleanup()

    def request(self, status=206, mutate=False):
        client = HTTPConnection(*self.server.server_address, timeout=3)
        with patch.object(cache.http.client, 'HTTPConnection') as upstream:
            response = upstream.return_value.getresponse.return_value
            response.status = status
            response.getheader.return_value = 'bytes 0-0/'+str(len(self.data))
            response.read.return_value = b'v'
            if mutate: self.file.write_bytes(b'changed content')
            client.request('GET', '/cube/template/artifact/download?artifact_id=rfs-abcd-1234&token=fixture',
                           headers={'X-Cube-Artifact-Token':'fixture'})
            received = client.getresponse()
            result = received.status, received.read()
            if status == 206 and not mutate:
                upstream.return_value.request.assert_called_once_with('GET',
                    '/cube/template/artifact/download?artifact_id=rfs-abcd-1234&token=fixture',
                    headers={'Range':'bytes=0-0','X-Cube-Artifact-Token':'fixture'})
            if mutate: upstream.assert_not_called()
        client.close()
        return result

    def test_native_authorization_required_before_cached_bytes(self):
        self.assertEqual(self.request(), (200, self.data))

    def test_upstream_rejection_does_not_serve_cache(self):
        status, body = self.request(status=403)
        self.assertEqual(status, 503)
        self.assertNotIn(self.data, body)

    def test_changed_cache_generation_fails_closed(self):
        status, _ = self.request(mutate=True)
        self.assertEqual(status, 503)


if __name__ == '__main__':
    unittest.main()
