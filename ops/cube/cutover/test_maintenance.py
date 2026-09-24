"""Exercise inert maintenance fragments in a separate loopback-only Caddy."""
import pathlib
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.error
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent


class MaintenanceRouting(unittest.TestCase):
    def exercise(self, fragment, cases):
        with socket.socket() as reserved:
            reserved.bind(("127.0.0.1", 0))
            port = reserved.getsockname()[1]
        with tempfile.TemporaryDirectory(prefix="cube-caddy-fixture-") as directory:
            config = pathlib.Path(directory) / "Caddyfile"
            config.write_text("{\n admin off\n auto_https off\n}\n"
                              f"http://127.0.0.1:{port} {{\n"
                              "route {\n"
                              f"import {HERE / fragment}\n"
                              # Use a named handle too, matching the production
                              # Cloudflare route that must not outrank the fence.
                              "@fixture header X-Fixture yes\n"
                              "handle @fixture {\n respond forwarded 200\n}\n"
                              "handle {\n respond forwarded 200\n}\n}\n}\n")
            with (pathlib.Path(directory) / "caddy.log").open("w+") as log:
                process = subprocess.Popen(["caddy", "run", "--config", str(config)],
                                           stdout=log, stderr=log)
                try:
                    deadline = time.monotonic() + 8
                    while True:
                        try:
                            with socket.create_connection(("127.0.0.1", port), timeout=.2):
                                break
                        except OSError:
                            if process.poll() is not None or time.monotonic() > deadline:
                                log.seek(0)
                                self.fail(log.read())
                            time.sleep(.05)
                    for method, path, expected in cases:
                        for named in (False, True):
                            with self.subTest(fragment=fragment, method=method, path=path, named=named):
                                headers = {"X-Fixture": "yes"} if named else {}
                                request = urllib.request.Request(f"http://127.0.0.1:{port}{path}", method=method, headers=headers)
                                try:
                                    response = urllib.request.urlopen(request, timeout=2)
                                except urllib.error.HTTPError as error:
                                    response = error
                                with response:
                                    self.assertEqual(response.status, expected)
                                    if expected == 503:
                                        self.assertEqual(response.headers.get("Retry-After"), "60")
                                        self.assertEqual(response.headers.get("Cache-Control"), "no-store")
                finally:
                    process.terminate()
                    process.wait(timeout=5)

    def test_drain(self):
        self.exercise("drain-platform.caddy", [
            ("GET", "/", 200), ("GET", "/api/projects", 200),
            ("POST", "/api/projects", 503), ("PUT", "/api/projects/x/files", 503),
            ("DELETE", "/api/projects/x", 503), ("POST", "/api/apps/x/remix", 503),
            ("POST", "/api/apps/x/preview", 503), ("POST", "/api/chat", 503),
            ("POST", "/api/voice", 503), ("POST", "/api/tools/call", 503),
            ("POST", "/api/admin/actions", 503), ("POST", "/api/bridge", 200),
            ("POST", "/api/v1/messages", 200), ("POST", "/api/platform/credit", 200),
        ])

    def test_offline(self):
        self.exercise("offline-platform.caddy", [
            ("GET", "/", 200), ("GET", "/api/projects/x/files", 503),
            ("GET", "/api/projects/x/tasks/t/events", 503),
            ("GET", "/api/apps/x/preview", 503), ("POST", "/api/bridge", 503),
            ("POST", "/api/projects", 503), ("POST", "/api/v1/messages", 200),
            ("GET", "/api/health", 200), ("GET", "/api/auth/google/start", 200),
        ])

    def test_preview_fence(self):
        self.exercise("offline-previews.caddy", [
            ("GET", "/", 503), ("POST", "/api/write", 503),
            ("GET", "/socket", 503),
        ])


if __name__ == "__main__":
    unittest.main()
