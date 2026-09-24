#!/usr/bin/env python3
"""Opt-in test of the installed Caddy log filter with non-secret sentinels."""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--caddy", default="caddy")
    args = parser.parse_args()
    if os.environ.get("CUBE_CADDY_LOG_TEST") != "1":
        raise SystemExit("explicit CUBE_CADDY_LOG_TEST=1 required")
    stanza = Path(__file__).with_name("caddy-preview-log-filter.caddy").read_text()
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    # The backend port is reserved but never listening, giving a deterministic
    # local connection refusal without contacting another service.
    with socket.socket() as denied, tempfile.TemporaryDirectory(prefix="caddy-log-fixture-") as directory:
        denied.bind(("127.0.0.1", 0))
        backend = denied.getsockname()[1]
        config = Path(directory, "Caddyfile")
        config.write_text("{\nadmin off\n" + stanza + "\n}\nhttp://127.0.0.1:" + str(port) + " {\nlog\nreverse_proxy 127.0.0.1:" + str(backend) + "\n}\n")
        log = Path(directory, "fixture.log")
        sentinel = "nonsecret-cube-preview-" + uuid.uuid4().hex
        with log.open("w") as output:
            process = subprocess.Popen([args.caddy, "run", "--config", str(config), "--adapter", "caddyfile"], stdout=output, stderr=output)
            try:
                request = urllib.request.Request(f"http://127.0.0.1:{port}/__sandboxd/preview-auth?token={sentinel}", headers={"Cookie": "sandbox_preview=" + sentinel, "Authorization": "Bearer " + sentinel, "Referer": "https://example.invalid/?token=" + sentinel})
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                for attempt in range(50):
                    if process.poll() is not None:
                        raise RuntimeError("isolated Caddy exited before readiness")
                    try:
                        opener.open(request, timeout=2)
                        raise AssertionError("expected synthetic upstream failure")
                    except urllib.error.HTTPError as e:
                        if e.code != 502:
                            raise AssertionError(f"expected 502, got {e.code}")
                        break
                    except urllib.error.URLError:
                        if attempt == 49:
                            raise
                        time.sleep(0.1)
                time.sleep(0.1)
            finally:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
        raw = log.read_text()
        if sentinel in raw:
            raise AssertionError("request capability leaked into isolated Caddy logs")
        records = []
        for line in raw.splitlines():
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError:
                pass
        for prefix in ("http.log.access", "http.log.error"):
            matching = [row for row in records if row.get("logger", "").startswith(prefix)]
            if not matching or any("uri" in row.get("request", {}) or "headers" in row.get("request", {}) for row in matching):
                raise AssertionError("missing or unfiltered " + prefix)
            if not all(row.get("request", {}).get("host") == f"127.0.0.1:{port}" for row in matching):
                raise AssertionError("request host lost from " + prefix)
        version = subprocess.check_output([args.caddy, "version"], text=True).strip()
        print(json.dumps({"caddy_version": version, "access_and_error_logs_redacted": True, "request_host_preserved": True, "production_configuration_changed": False}))


if __name__ == "__main__":
    main()
