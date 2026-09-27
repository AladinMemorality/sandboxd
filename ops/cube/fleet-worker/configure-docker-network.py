#!/usr/bin/env python3
"""Give Docker inside the fresh worker a subnet distinct from its outer host."""
import json
import os
from pathlib import Path
import subprocess


def output(*args):
    return subprocess.check_output(args, text=True).strip()


if os.geteuid() != 0 or output("hostname") != "baarcha-cube-worker-b200-01":
    raise RuntimeError("not the candidate worker VM")
if Path("/proc/sys/kernel/random/boot_id").read_text().strip() != "c5a2d884-8a99-4b5b-8a5d-9e79654df4b8":
    raise RuntimeError("worker boot changed; repeat the network review")
if output("docker", "ps", "-aq"):
    raise RuntimeError("worker already has containers; drain before changing Docker networking")
config = Path("/etc/docker/daemon.json")
if config.exists() or config.is_symlink():
    raise RuntimeError("existing Docker configuration requires review")
routes = json.loads(output("ip", "-j", "route", "show"))
if not any(r.get("dst") == "172.17.0.0/16" and r.get("dev") == "docker0" for r in routes):
    raise RuntimeError("default bridge differs from the reviewed overlap")
subprocess.run(["systemctl", "stop", "docker.service", "docker.socket"], check=True)
subprocess.run(["ip", "link", "delete", "docker0"], check=True)
fd = os.open(config, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w") as stream:
    json.dump({"bip": "10.253.0.1/24", "default-address-pools": [{"base": "10.253.128.0/17", "size": 24}]}, stream)
    stream.write("\n")
    stream.flush()
    os.fsync(stream.fileno())
subprocess.run(["systemctl", "start", "docker.service"], check=True)
print(output("ip", "-j", "route", "show"))
