#!/usr/bin/env python3
"""Prepare a new, narrowly routed management link; never print private keys."""
import base64
import ipaddress
import json
import os
from pathlib import Path
import stat
import subprocess
import sys


def output(*args):
    return subprocess.check_output(args, text=True).strip()


def main():
    if os.geteuid() != 0 or len(sys.argv) != 3 or sys.argv[1] not in ("server", "worker"):
        raise RuntimeError("usage: configure-link.py server|worker PEER_PUBLIC_KEY as root")
    role, peer = sys.argv[1:]
    if len(base64.b64decode(peer, validate=True)) != 32:
        raise RuntimeError("invalid peer public key")
    if role == "worker":
        if output("hostname") != "baarcha-cube-worker-b200-01":
            raise RuntimeError("wrong worker")
        address, allowed = "10.254.240.2/32", "10.254.240.1/32"
        extra = "Endpoint = 10.40.14.69:51827\nPersistentKeepalive = 25\n"
        listen = ""
    else:
        addresses = json.loads(output("ip", "-j", "addr", "show"))
        if not any(a.get("local") == "65.108.225.153" for interface in addresses for a in interface.get("addr_info", [])):
            raise RuntimeError("wrong coordinator host")
        address, allowed = "10.254.240.1/24", "10.254.240.2/32"
        extra, listen = "", "ListenPort = 51827\n"
    network = ipaddress.ip_network("10.254.240.0/24")
    for route in json.loads(output("ip", "-j", "-4", "route", "show", "table", "all")):
        dst = route.get("dst", "default")
        if dst != "default" and network.overlaps(ipaddress.ip_network(dst, strict=False)):
            raise RuntimeError("management network overlaps an existing route")
    keyfd = os.open("/etc/wireguard/cube-fleet-private.key", os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(keyfd) as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_size > 64:
            raise RuntimeError("private root-owned key required")
        private = stream.read(65).strip()
    if len(base64.b64decode(private, validate=True)) != 32:
        raise RuntimeError("invalid private key file")
    config = f"[Interface]\nAddress = {address}\nMTU = 1380\nPrivateKey = {private}\n{listen}\n[Peer]\nPublicKey = {peer}\nAllowedIPs = {allowed}\n{extra}"
    fd = os.open("/etc/wireguard/wg-cube-fleet.conf", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as stream:
        stream.write(config)
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({"prepared": True, "role": role, "address": address, "default_route_changed": False}))


if __name__ == "__main__":
    main()
