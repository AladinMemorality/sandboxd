"""Exact online VPS data growth; no reboot, disk replacement, or guard change."""
import fcntl, json, os, pathlib, socket, subprocess, time
os.umask(0o077)
root = pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007')
disk = pathlib.Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2')
target = 512 * 1024**3
locks = []
for p in ['/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock', '/run/lock/cube-operator-acceptance.lock']:
    f = open(p, 'a'); fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB); locks.append(f)
assert subprocess.check_output(['docker','inspect','-f','{{.State.Running}}','src-sandboxd-1'], text=True).strip() == 'false'
ssh = ['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-o','BatchMode=yes','-p','20222','root@127.0.0.1']
def guest(command): return subprocess.check_output(ssh + [command], text=True).strip()
boot = guest('cat /proc/sys/kernel/random/boot_id')
assert boot == 'ae6250ea-8933-416b-9621-cbb1eadfc54b'
identity = guest('findmnt -no SOURCE,FSTYPE,UUID /data').split()
assert identity == ['/dev/vdb','xfs','793c3349-db9c-4815-9842-989ed484f1f8']
st = disk.stat(); assert st.st_ino == 3932163 and not disk.is_symlink()
sock = socket.socket(socket.AF_UNIX); sock.settimeout(15); sock.connect('/opt/baarcha-cube/worker-01/qmp.sock')
stream = sock.makefile('rwb', buffering=0); json.loads(stream.readline())
def qmp(command, args=None):
    request = {'execute':command}
    if args is not None: request['arguments'] = args
    stream.write((json.dumps(request)+'\n').encode())
    while True:
        r = json.loads(stream.readline())
        if 'error' in r: raise RuntimeError(r)
        if 'return' in r: return r['return']
qmp('qmp_capabilities'); assert qmp('query-status')['running']
def block():
    matches = [b for b in qmp('query-block') if b.get('inserted',{}).get('file') == str(disk)]
    assert len(matches) == 1 and matches[0]['device'] == 'virtio1'
    b = matches[0]; assert b['io-status'] == 'ok' and not b['inserted']['ro']
    return b
before = block(); assert before['inserted']['image']['virtual-size'] == 448 * 1024**3
fs = os.statvfs(disk.parent); free = fs.f_bavail * fs.f_frsize
worst_headroom = free - max(0, target - st.st_blocks * 512)
assert worst_headroom > 128 * 1024**3
receipt = {'before':before,'inode':st.st_ino,'boot':boot,'identity':identity,'target_bytes':target,'host_free_bytes':free,'host_headroom_if_full_bytes':worst_headroom,'at':time.time()}
with (root/'data-growth-intent.json').open('x') as f:
    json.dump(receipt,f); f.flush(); os.fsync(f.fileno())
qmp('block_resize', {'device':'virtio1','size':target})
assert block()['inserted']['image']['virtual-size'] == target
assert int(guest('blockdev --getsize64 /dev/vdb')) == target
receipt['grow_output'] = guest('flock -n /run/lock/cube-operator-acceptance.lock xfs_growfs /data')
assert guest('cat /proc/sys/kernel/random/boot_id') == boot
assert guest('findmnt -no SOURCE,FSTYPE,UUID /data').split() == identity
assert disk.stat().st_ino == st.st_ino
receipt['after'] = block(); receipt['guest_df'] = guest('df -B1 /data')
with (root/'data-growth-complete.json').open('x') as f:
    json.dump(receipt,f); f.flush(); os.fsync(f.fileno())
print(json.dumps({'grown_gib':512,'same_boot_disk_and_uuid':True,'guest_df':receipt['guest_df']}))
