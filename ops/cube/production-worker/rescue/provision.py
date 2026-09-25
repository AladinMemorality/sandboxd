#!/usr/bin/env python3
"""Provision one disposable, isolated rescue appliance; never start it implicitly.

Only the verified clean Ubuntu input is opened. No customer/worker disk is
attached. The coordinator must separately review resources before starting.
"""
import hashlib
import json
import os
from pathlib import Path
import pwd
import shutil
import socket
import subprocess

BASE = Path('/opt/baarcha-cube/worker-01/input/noble-server-cloudimg-amd64.img')
SHA = '612b2c0cc1bc413a6cb8c38fd611794caf0f2b436c50013d8b3794db12ad7354'
ROOT = Path('/var/lib/baarcha-cube-rescue-20260925')
KEYS = Path('/opt/baarcha-cube/rescue-operator-20260925')
UNIT = Path('/etc/systemd/system/baarcha-cube-rescue-20260925.service')
USER = 'baarcha-cube-rescue'


def run(*args):
    subprocess.run(args, check=True, timeout=120, stdout=subprocess.DEVNULL)


def main():
    assert os.geteuid() == 0
    assert Path('/dev/kvm').exists()
    assert not ROOT.exists() and not KEYS.exists() and not UNIT.exists()
    with BASE.open('rb') as f:
        assert hashlib.file_digest(f, 'sha256').hexdigest() == SHA
    assert shutil.disk_usage('/var/lib').free > 50 * 1024**3
    with socket.socket() as s:
        s.bind(('127.0.0.1', 20223))
    try:
        pwd.getpwnam(USER)
    except KeyError:
        run('useradd', '--system', '--no-create-home', '--shell', '/usr/sbin/nologin', USER)
    user = pwd.getpwnam(USER)
    assert user.pw_uid != 0 and user.pw_shell == '/usr/sbin/nologin'
    ROOT.mkdir(mode=0o700)
    KEYS.mkdir(mode=0o700)
    run('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(KEYS / 'operator-key'))
    cloud = {'hostname': 'baarcha-cube-rescue', 'manage_etc_hosts': True,
             'ssh_pwauth': False, 'disable_root': False,
             'users': [{'name': 'root', 'lock_passwd': True,
                        'ssh_authorized_keys': [(KEYS / 'operator-key.pub').read_text().strip()]}],
             'package_update': False, 'package_upgrade': False}
    (ROOT / 'user-data').write_text('#cloud-config\n' + json.dumps(cloud) + '\n')
    (ROOT / 'meta-data').write_text(json.dumps({'instance-id': ROOT.name, 'local-hostname': 'baarcha-cube-rescue'}))
    run('cloud-localds', str(ROOT / 'seed.img'), str(ROOT / 'user-data'), str(ROOT / 'meta-data'))
    run('qemu-img', 'convert', '-f', 'qcow2', '-O', 'qcow2', str(BASE), str(ROOT / 'root.qcow2'))
    run('qemu-img', 'resize', str(ROOT / 'root.qcow2'), '40G')
    for path in ROOT.iterdir():
        path.chmod(0o600)
        os.chown(path, user.pw_uid, user.pw_gid)
    os.chown(ROOT, user.pw_uid, user.pw_gid)
    command = ['/usr/bin/qemu-system-x86_64', '-enable-kvm', '-machine', 'q35,accel=kvm',
               '-cpu', 'host', '-name', 'baarcha-cube-rescue', '-m', '3072', '-smp', '2',
               '-device', 'virtio-rng-pci', '-drive', f'file={ROOT}/root.qcow2,if=virtio,format=qcow2',
               '-drive', f'file={ROOT}/seed.img,if=virtio,format=raw,readonly=on',
               '-nic', 'user,model=virtio-net-pci,restrict=on,ipv6=off,hostfwd=tcp:127.0.0.1:20223-:22',
               '-display', 'none', '-serial', f'file:{ROOT}/serial.log',
               '-qmp', f'unix:{ROOT}/qmp.sock,server=on,wait=off']
    UNIT.write_text(f'''[Unit]
Description=Disposable isolated Cube filesystem rescue appliance
ConditionPathExists=/dev/kvm
[Service]
Type=simple
User={USER}
SupplementaryGroups=kvm
UMask=0077
ExecStart={' '.join(command)}
Restart=no
MemoryMax=4G
MemorySwapMax=0
CPUQuota=200%
TasksMax=128
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths={ROOT}
DevicePolicy=closed
DeviceAllow=/dev/kvm rw
RestrictAddressFamilies=AF_UNIX AF_INET
TimeoutStopSec=30
KillMode=control-group
''')
    run('systemd-analyze', 'verify', str(UNIT))
    run('systemctl', 'daemon-reload')
    proof = {'base_sha256': SHA, 'started': False, 'customer_disks_attached': False,
             'qemu_user': USER, 'guest_ram_mib': 3072, 'vcpus': 2,
             'network': 'restricted user network, IPv6 disabled, only loopback SSH20223 forwarded',
             'unit_sha256': hashlib.sha256(UNIT.read_bytes()).hexdigest()}
    (KEYS / 'provisioning.json').write_text(json.dumps(proof, indent=2) + '\n')
    print(json.dumps(proof))


if __name__ == '__main__':
    main()
