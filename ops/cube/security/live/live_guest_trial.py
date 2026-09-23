#!/usr/bin/env python3
"""Run the network workload in ONE disposable pre-created guest; never change a worker.

The caller must have installed the patched worker, created two disposable guests
from the reviewed template, and enabled ONLY registry.npmjs.org and each target's
exact rebinding-test domain in this guest's Cube network policy. This script
replaces pilot-probe.mjs and restarts that disposable guest's supervisor. It then
pauses/resumes that guest once. Production guests must never be supplied.
"""
import argparse
import json
import pathlib
import re
import time
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--credentials", type=pathlib.Path, required=True)
parser.add_argument("--config", type=pathlib.Path, required=True)
parser.add_argument("--output", type=pathlib.Path, required=True)
parser.add_argument("--cube-api", default="http://127.0.0.1:3000")
parser.add_argument("--cube-proxy", default="http://127.0.0.1:80")
args = parser.parse_args()
credentials = json.loads(args.credentials.read_text())
assert args.credentials.stat().st_mode & 0o077 == 0, "credential file must be private"
config = json.loads(args.config.read_text())
sid = credentials["sandbox_id"]
assert re.fullmatch(r"[A-Za-z0-9-]{1,50}", sid)
assert 1 <= len(config["targets"]) <= 16


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *unused, **kw):
        return None


opener = urllib.request.build_opener(NoRedirect)


def request(method, path, payload=None, management=False):
    if management:
        base = args.cube_api
        headers = {"X-API-Key": credentials["cube_api_key"]}
    else:
        base = args.cube_proxy
        headers = {"Host": f"3031-{sid}.cube.app", "Authorization": "Bearer " + credentials["supervisor_token"], "Cube-Traffic-Access-Token": credentials["traffic_access_token"]}
    if isinstance(payload, dict):
        payload = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
    try:
        with opener.open(urllib.request.Request(base + path, data=payload, headers=headers, method=method), timeout=140) as response:
            return response.read()
    except urllib.error.HTTPError as error:
        # Never dump request headers, tokens, or upstream bodies to test logs.
        raise RuntimeError(f"probe request failed with HTTP {error.code}") from None


def read_report(after):
    until = time.monotonic() + 45
    while time.monotonic() < until:
        try:
            report = json.loads(request("GET", "/files/content?path=network-probe-result.json"))
            if report["timestamp"] > after:
                return report
        except (OSError, ValueError, RuntimeError):
            pass
        time.sleep(1)
    raise RuntimeError("network probe did not produce a fresh report")


def validate(report):
    assert report["registry"]["succeeded"], "public registry positive control failed"
    assert not report["unapprovedDomain"]["connected"], "unapproved domain connected"
    for target in report["targets"]:
        assert target["ip"] in target["answers"], "rebinding DNS setup was not proven"
        assert not target["direct"]["connected"], "protected numeric destination reachable"
        assert not target["rebound"]["connected"], "protected DNS destination reachable"
    assert not report["ipv6"]["connected"], "native IPv6 destination reachable"
    assert not report["forgedUDPSourcePort"]["received"], "forged UDP source port reached canary"
    assert report["forgedTCPSourcePort"]["error"] not in ("EADDRINUSE", "EACCES"), "TCP source-port test did not reach network"
    assert not report["forgedTCPSourcePort"]["connected"], "forged TCP source port reached canary"
    assert report["rawPacketProbeRan"] and report["rawPacketCreationDenied"], "unprivileged raw packet boundary not established"


report = {"scope": "one disposable guest before/after pause; check canary log and raw/fragment kernel probes separately"}
manifest = request("GET", "/files/content?path=sandbox.yaml")
assert b"--port 3000" in manifest or b"--port 3005" in manifest
request("PUT", "/files?path=sandbox.yaml", manifest.replace(b"3000", b"3005"))
request("PUT", "/files?path=network-probe-config.json", json.dumps(config).encode())
request("PUT", "/files?path=pilot-probe.mjs", pathlib.Path(__file__).with_name("live-network-probe.mjs").read_bytes())
started = int(time.time() * 1000)
request("POST", "/config", {"env": {}, "revision": "network-proof-" + str(started)})
try:
    report["before"] = read_report(started)
    validate(report["before"])
    request("POST", f"/sandboxes/{sid}/pause", {}, management=True)
    time.sleep(1)
    request("POST", f"/sandboxes/{sid}/connect", {}, management=True)
    report["after_resume"] = read_report(int(time.time() * 1000))
    validate(report["after_resume"])
    report["passed"] = True
finally:
    args.output.write_text(json.dumps(report, indent=2) + "\n")
