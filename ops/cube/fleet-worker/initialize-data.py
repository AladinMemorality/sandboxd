#!/usr/bin/env python3
"""Initialize only the verified, empty data disk in the fresh B200 worker VM."""
import json
import os
from pathlib import Path
import subprocess


def output(*args):
    return subprocess.check_output(args, text=True).strip()


def main():
    if os.geteuid() != 0 or output("hostname") != "baarcha-cube-worker-b200-01":
        raise RuntimeError("not the reviewed worker VM")
    if Path("/proc/sys/kernel/random/boot_id").read_text().strip() != "c5a2d884-8a99-4b5b-8a5d-9e79654df4b8":
        raise RuntimeError("worker boot changed; review the disk identity again")
    disk = Path("/dev/disk/by-id/virtio-baarcha-b200-data").resolve(strict=True)
    if disk != Path("/dev/vdb") or output("blockdev", "--getsize64", str(disk)) != "274877906944":
        raise RuntimeError("unexpected data disk identity or size")
    row = json.loads(output("lsblk", "--json", "-o", "NAME,TYPE,FSTYPE,MOUNTPOINTS,SERIAL", str(disk)))["blockdevices"]
    if len(row) != 1 or row[0].get("serial") != "baarcha-b200-data" or row[0].get("type") != "disk" or row[0].get("fstype") or row[0].get("children") or any(row[0].get("mountpoints", [])):
        raise RuntimeError("data disk is not empty and unmounted")
    data = Path("/data")
    if data.is_symlink() or (data.exists() and any(data.iterdir())):
        raise RuntimeError("data directory is not empty")
    fstab = Path("/etc/fstab")
    if any(len(line.split()) > 1 and line.split()[1] == "/data" for line in fstab.read_text().splitlines() if not line.lstrip().startswith("#")):
        raise RuntimeError("data mount already configured")
    data.mkdir(mode=0o755, exist_ok=True)
    subprocess.run(["mkfs.xfs", "-m", "reflink=1", "-L", "cube-b200", str(disk)], check=True)
    uuid = output("blkid", "-s", "UUID", "-o", "value", str(disk))
    subprocess.run(["mount", "-t", "xfs", "-o", "noatime,prjquota", str(disk), "/data"], check=True)
    with fstab.open("a") as stream:
        stream.write(f"\nUUID={uuid} /data xfs defaults,noatime,prjquota 0 2\n")
        stream.flush()
        os.fsync(stream.fileno())
    subprocess.run(["xfs_info", "/data"], check=True)
    print(json.dumps({"initialized": True, "guest_disk": str(disk), "uuid": uuid, "mount": "/data"}))


if __name__ == "__main__":
    main()
