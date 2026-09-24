#!/usr/bin/env python3
"""Read-only Linux placement inventory; does not attest isolation or activate Cube."""
import json
import os
from pathlib import Path
import platform
import shutil
import socket
import subprocess
import time


def read(path):
    try:
        return Path(path).read_text().strip()
    except OSError:
        return None


def command(args):
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=10)
        return result.stdout.strip() if result.returncode == 0 else None
    except (OSError, subprocess.TimeoutExpired):
        return None


memory = {}
for line in (read("/proc/meminfo") or "").splitlines():
    key, value = line.split(":", 1)
    if key in ("MemTotal", "MemAvailable", "SwapTotal", "SwapFree"):
        memory[key + "_kib"] = int(value.split()[0])
disks = {}
for path in ("/", "/mnt/nvme", "/data"):
    if Path(path).exists():
        total, used, free = shutil.disk_usage(path)
        disks[path] = {"total": total, "used": used, "free": free,
                       "filesystem": command(["findmnt", "-n", "-o", "FSTYPE", "-T", path])}
listeners = {}
for port in (20222, 20300, 20080):
    # Connect only to the intended loopback listeners. No protocol payload.
    sock = socket.socket()
    sock.settimeout(0.1)
    try:
        listeners[str(port)] = sock.connect_ex(("127.0.0.1", port)) == 0
    finally:
        sock.close()
print(json.dumps({
    "observed_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "scope": "read-only placement inventory; no production acceptance",
    "kernel": platform.release(), "machine": platform.machine(), "logical_cpus": os.cpu_count(),
    "memory": memory, "disks": disks, "planned_ports_in_use": listeners,
    "kvm_present": Path("/dev/kvm").exists(), "tun_present": Path("/dev/net/tun").exists(),
    "nested_amd": read("/sys/module/kvm_amd/parameters/nested"),
    "nested_intel": read("/sys/module/kvm_intel/parameters/nested"),
    "cgroup_filesystem": command(["stat", "-f", "-c", "%T", "/sys/fs/cgroup"]),
    "cgroup_controllers": read("/sys/fs/cgroup/cgroup.controllers"),
    "cgroup_enabled": read("/sys/fs/cgroup/cgroup.subtree_control"),
    "bpf_supported": "bpf" in (read("/proc/filesystems") or "").split(),
    "bpf_mount": command(["findmnt", "-n", "-o", "FSTYPE", "/sys/fs/bpf"]),
    "qemu_version": command(["qemu-system-x86_64", "--version"]),
    "production_vm_already_present": Path("/opt/baarcha-cube/worker-01").exists(),
    "production_unit_already_present": Path("/etc/systemd/system/baarcha-cube-worker-01.service").exists(),
}, indent=2))
