#!/usr/bin/env python3
"""Render private, inert worker provisioning files. Never run external commands.

Does not download, provision disks, install packages, launch a VM, or enable Cube.
The output directory must not exist. See README.md before using any artifact.
"""
import argparse
import json
import os
from pathlib import Path
import re
import secrets

RELEASE = {
    "version": "v0.7.1",
    "source_commit": "31d911e430fdf8a8879bd062b8e78066c1a8e89d",
    "url": "https://github.com/TencentCloud/CubeSandbox/releases/download/v0.7.1/cube-sandbox-one-click-v0.7.1-amd64.tar.gz",
    "sha256": "a516ed71e90e273d03f053a26bbbf3932e0d747387fbccd25ec76f89b20bc0a1",
}


def render(output, public_key, name="worker-01"):
    if not re.fullmatch(r"worker-[a-z0-9]{1,24}", name):
        raise ValueError("name must be worker- followed by 1..24 lowercase letters/digits")
    public_key = public_key.strip()
    if not re.fullmatch(r"ssh-ed25519 [A-Za-z0-9+/]+={0,2}(?: [^\r\n]+)?", public_key):
        raise ValueError("expected one OpenSSH Ed25519 public key; never supply a private key")
    output = Path(output).absolute()
    if output.exists() or output.is_symlink():
        raise ValueError("output must be a new directory; existing credentials are never overwritten")
    if any("bench" in part.lower() for part in output.parts):
        raise ValueError("do not stage production files inside a benchmark directory")
    output.mkdir(mode=0o700, parents=False)
    os.chmod(output, 0o700)

    def write(name, data):
        fd = os.open(output / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write(data)

    root = f"/opt/baarcha-cube/{name}"
    data = f"/mnt/nvme/baarcha-cube/{name}"
    creds = {key: secrets.token_hex(32) for key in (
        "api", "mysql_root", "mysql", "redis", "minio", "jwt", "template_callback")}
    env = {
        "ONE_CLICK_DEPLOY_ROLE": "control", "ONE_CLICK_RUN_QUICKCHECK": "1",
        "CUBE_PVM_ENABLE": "0", "ONE_CLICK_ENABLE_S3LVOL": "0",
        "CUBE_SANDBOX_NODE_IP": "10.0.2.15", "CUBE_API_BIND": "0.0.0.0:3000",
        "CUBE_API_HEALTH_ADDR": "127.0.0.1:3000", "CUBE_API_SANDBOX_DOMAIN": "cube.app",
        "CUBE_API_KEY": creds["api"], "CUBEMASTER_HTTP_BIND": "0.0.0.0",
        "CUBE_OPS_BIND": "127.0.0.1:3010", "CUBE_OPS_ADDR": "127.0.0.1:3010", "WEB_UI_ENABLE": "0",
        "CUBE_SANDBOX_MYSQL_ROOT_PASSWORD": creds["mysql_root"],
        "CUBE_SANDBOX_MYSQL_DB": "cube_mvp", "CUBE_SANDBOX_MYSQL_USER": "cube",
        "CUBE_SANDBOX_MYSQL_PASSWORD": creds["mysql"],
        "CUBE_SANDBOX_REDIS_PASSWORD": creds["redis"],
        "CUBE_SANDBOX_MINIO_ROOT_USER": "cubeprod", "CUBE_SANDBOX_MINIO_ROOT_PASSWORD": creds["minio"],
        "JWT_SECRET": creds["jwt"], "CUBE_TEMPLATE_CALLBACK_TOKEN": creds["template_callback"],
        "DATABASE_URL": f"mysql://cube:{creds['mysql']}@127.0.0.1:3306/cube_mvp",
        "CUBE_PROXY_DNS_ENABLE": "1", "CUBE_PROXY_RESOLVED_DNS_ADDR": "169.254.254.53",
        "ONE_CLICK_ENABLE_TENCENT_DOCKER_MIRROR": "0",
    }
    write("cube-install.env", "# PRIVATE: fresh credentials; use only inside the new worker VM.\n" +
          "\n".join(f"{k}={v}" for k, v in env.items()) + "\n")
    cloud = {"hostname": "baarcha-cube-" + name, "manage_etc_hosts": True,
             "ssh_pwauth": False, "disable_root": False,
             "users": [{"name": "root", "lock_passwd": True, "ssh_authorized_keys": [public_key]}],
             "package_update": False, "package_upgrade": False}
    write("user-data", "#cloud-config\n" + json.dumps(cloud, indent=2) + "\n")
    write("meta-data", json.dumps({"instance-id": "baarcha-cube-" + name,
                                  "local-hostname": "baarcha-cube-" + name}) + "\n")
    # No public listeners or host bridge/TAP/iptables changes. Host root is never
    # shared into the VM: only the three explicitly dedicated virtual disks.
    command = ["/usr/bin/qemu-system-x86_64", "-enable-kvm", "-machine", "q35,accel=kvm",
               "-cpu", "host", "-name", "baarcha-cube-" + name, "-m", "40960", "-smp", "12",
               "-device", "virtio-rng-pci",
               "-drive", f"file={root}/root.qcow2,if=virtio,format=qcow2",
               "-drive", f"file={data}/data.qcow2,if=virtio,format=qcow2",
               "-drive", f"file={root}/seed.img,if=virtio,format=raw,readonly=on",
               "-nic", "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:20222-:22,hostfwd=tcp:127.0.0.1:20300-:3000,hostfwd=tcp:127.0.0.1:20080-:80",
               "-display", "none", "-serial", "file:" + root + "/serial.log",
               "-qmp", "unix:" + root + "/qmp.sock,server=on,wait=off"]
    write("baarcha-cube-" + name + ".service", """# INERT CANDIDATE: do not install/start until the reviewed provisioning steps.
[Unit]
Description=Baarcha dedicated Cube worker VM
After=network-online.target
Wants=network-online.target
RequiresMountsFor=""" + root + " " + data + """
ConditionPathExists=/dev/kvm

[Service]
Type=simple
User=root
UMask=0077
ExecStart=""" + " ".join(command) + """
Restart=no
MemoryHigh=42G
MemoryMax=44G
MemorySwapMax=0
CPUQuota=1000%
TimeoutStopSec=180
# Before stopping, pause/drain guest workloads and issue QMP system_powerdown.
# Killing QEMU or reaching TimeoutStopSec is crash recovery, not a clean backup.
KillMode=control-group

[Install]
WantedBy=multi-user.target
""")
    write("plan.json", json.dumps({
        "status": "prepared_only", "production_activation": False, "name": name,
        "release": RELEASE, "vm_root": root, "vm_data": data,
        "root_disk_gib": 120, "data_disk_gib": 320, "memory_mib": 40960, "vcpus": 12,
        "ports": {"ssh": 20222, "api": 20300, "proxy": 20080},
        "listener_address": "127.0.0.1", "guest_ipv4": "10.0.2.15",
        "guest_direct_egress": "deny_all", "worker_binary": "UNSELECTED",
        "ubuntu_cloud_image_sha256": "UNSELECTED", "dependency_image_digests": "UNSELECTED",
        "fresh_template_ids": "UNREGISTERED", "platform_transport": "UNCONFIGURED",
        "capacity": {"observed_fleet_projects": 61, "observed_running_projects": 24,
                     "planned_active_and_waking_guests": 12, "host_guest_memory_quota_mib": 30720,
                     "host_guest_cpu_quota_millicores": 28000,
                     "guest_memory_mib": 2048, "guest_cpu_millicores": 2000,
                     "controller_and_margin_mib": 10240,
                     "is_enforced_admission_limit": False},
    }, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--ssh-public-key", required=True, type=Path)
    parser.add_argument("--name", default="worker-01")
    args = parser.parse_args()
    try:
        render(args.output, args.ssh_public_key.read_text(), args.name)
    except (ValueError, OSError) as exc:
        parser.error(str(exc))
    print("Prepared private, inert files. No VM, service, network or routing changes.")
