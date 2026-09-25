#!/usr/bin/env python3
"""Prepare a fresh retained-source journal stage; never start services or guests.

Run on the outer operator host only after independent current-disk capture,
rescue conversion, and a fresh operator execution-fence receipt. This does not
copy the live disk, mint fencing proof or execute the generated run command.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import time
import urllib.request

SSH = ['ssh', '-i', '/opt/baarcha-cube/worker-01/operator-key', '-p20222',
       '-oBatchMode=yes', '-oConnectTimeout=10', '-oServerAliveInterval=15',
       '-oServerAliveCountMax=2', '-oStrictHostKeyChecking=yes',
       '-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts', 'root@127.0.0.1']


def require(ok, message):
    if not ok:
        raise ValueError(message)


def digest(path):
    result = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(4 << 20), b''):
            result.update(chunk)
    return result.hexdigest()


def private(path):
    path = Path(path)
    for part in [path, *path.parents]:
        require(not part.is_symlink(), 'private path contains link')
    st = path.stat()
    require(stat.S_ISREG(st.st_mode) and st.st_uid == 0 and st.st_mode & 0o077 == 0,
            'root-owned private regular input required')
    require(st.st_size <= 8 << 20, 'metadata input too large')
    return json.loads(path.read_text())


def write(path, value):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as stream:
        json.dump(value, stream, indent=2)
        stream.flush()
        os.fsync(stream.fileno())


def validate_fence(fence, old, owned, boot, now):
    require(fence.get('purpose') == 'OWNED_RECOVERY_EXECUTION_FENCE' and
            fence.get('old_provider_id') == owned and
            fence.get('worker_machine_id') == old['worker_machine_id'] and
            fence.get('previous_boot_id') == old['worker_boot_id'] and
            fence.get('current_boot_id') == boot and boot != old['worker_boot_id'],
            'exact source/worker boot fence required')
    require(all(fence.get(key) is True for key in ['no_task_verified',
            'no_owned_vmm_or_disk_fd_verified', 'provider_requests_drained', 'management_fenced']),
            'explicit operator execution fencing required')
    require(now - 300 <= fence.get('checked_at', 0) <= now and
            now < fence.get('expires_at', 0) <= now + 1200, 'fresh bounded fence required')


def validate_inventory(text, owned):
    require(re.findall(r'^\s*SANDBOX_COUNT\s+(\d+)\s*$', text, re.M) == ['1'] and
            len(re.findall(r'^\s*NODES_SCANNED\s+1/1\s*$', text, re.M)) == 1,
            'complete old-only Master inventory required')
    ids = re.findall(r'^\s*([a-f0-9]{32})\s+', text, re.M)
    require(ids == [owned], 'foreign or duplicated runtime inventory')


def validate_capture_metadata(plan, manifest, metadata):
    require(plan['source_metadata_sha256'] == manifest['source_metadata_sha256'],
            'captured metadata differs from actual input manifest')
    for name in ['cubebox', 'storage']:
        value_sha = hashlib.sha256(json.dumps(metadata[name], sort_keys=True,
                                  separators=(',', ':')).encode()).hexdigest()
        require(value_sha == plan['source_metadata_sha256'][name],
                'capture metadata canonical hash mismatch')


def stage_converted(source, output):
    destination = output / 'converted'
    destination.mkdir(mode=0o700)
    for name in ['app.zip', 'home.zip', 'home-manifest.json', 'conversion.json']:
        original = source / name
        info = original.lstat()
        require(stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid() and
                info.st_mode & 0o077 == 0 and info.st_size < 256 << 20,
                'private bounded converted file required')
        with original.open('rb') as inp, (destination / name).open('xb') as out:
            os.fchmod(out.fileno(), 0o600)
            shutil.copyfileobj(inp, out)
            out.flush(); os.fsync(out.fileno())
        require(digest(original) == digest(destination / name), 'converted staging digest mismatch')
    return destination


def journal_argv(binary, old_dir, output):
    require(output.is_absolute() and output.parent == Path('/opt/baarcha-bench') and
            output.name.startswith('cube-journal-'), 'journal output outside executable guard')
    return [binary, 'run', str(old_dir), str(output / 'converted'), str(output)]


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("management redirect refused")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['crash-stage', 'capture-metadata', 'capture-input', 'capture-fence', 'export-stage',
                 'execution-fence', 'output', 'binary', 'binary-sha256', 'owned-id']:
        parser.add_argument('--' + name, required=True)
    parser.add_argument("--post-capture-reboot")
    args = parser.parse_args()
    require(os.geteuid() == 0, 'outer operator root required')
    owned = args.owned_id
    require(re.fullmatch('[a-f0-9]{32}', owned) is not None, 'exact provider ID required')
    metadata = Path(args.capture_metadata)
    old_dir, inputs, export, output = map(Path, [args.crash_stage, args.capture_input,
                                               args.export_stage, args.output])
    require(output.is_absolute() and str(output) == os.path.normpath(output) and
            str(output).startswith('/opt/baarcha-bench/cube-journal-') and
            output.parent == Path('/opt/baarcha-bench'), 'isolated journal output required')
    old = private(old_dir / 'escrow.private.json')
    report = private(old_dir / 'report.json')
    require(old['guest']['sandboxID'] == owned and report.get('sandbox_id') == owned and
            report.get('checkpoint_ready') is True and report.get('latest') == old['latest'] and
            report.get('post_power_loss_latest_app_home_sql_preserved') is not True,
            'actual native failure and acknowledged source required')
    require(digest(args.binary) == args.binary_sha256, 'candidate binary hash mismatch')
    def worker(command):
        return subprocess.check_output(SSH + [command], text=True, timeout=45,
                                       stderr=subprocess.DEVNULL).strip()
    boot = worker('cat /proc/sys/kernel/random/boot_id')
    require(worker('hostname') == 'baarcha-cube-worker-01' and
            worker('cat /etc/machine-id') == old['worker_machine_id'] and
            worker('findmnt -n -o UUID /data') == old['data_uuid'], 'worker identity changed')
    fence = private(args.execution_fence)
    now = int(time.time())
    validate_fence(fence, old, owned, boot, now)
    validate_inventory(worker('cubemastercli -a 127.0.0.1 list --all --wide'), owned)
    require(worker('timeout 5 ctr --address /data/cubelet/cubelet.sock --namespace default tasks list 2>/dev/null').split()
            == ['TASK', 'PID', 'STATUS'], 'worker tasks are not empty')
    env = Path('/opt/baarcha-cube/worker-01/staging/cube-install.env').read_text()
    key = next(line.split('=', 1)[1].strip().strip('\"\'') for line in env.splitlines()
               if line.startswith('CUBE_API_KEY='))
    request = urllib.request.Request('http://127.0.0.1:20300/sandboxes/' + owned,
                                     headers={'X-API-Key': key})
    with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect()).open(request, timeout=10) as response:
        remote = json.loads(response.read(1 << 20))
    require(remote.get('sandboxID') == owned and remote.get('templateID') == old['guest']['templateID'] and
            remote.get('cpuCount') == 2 and remote.get('memoryMB') == 2048 and
            remote.get('state') in ('unknown', 'stopped') and
            remote.get('metadata', {}).get('operator-crash-fixture') == old['fixture'],
            'retained provider immutable identity or truthful state mismatch')
    exported = private(export / 'export-report.json')
    conversion = private(export / 'converted' / 'conversion.json')
    require(exported['sandbox_id'] == owned and exported['archive_sha256'] == digest(export / 'home.tar') and
            conversion['source_archive_sha256'] == exported['archive_sha256'] and
            conversion['go_import_contract_validated'] is True, 'current archive conversion mismatch')
    plan = private(metadata / 'plan.json')
    manifest = private(inputs / 'rescue-input.json')
    require(plan['sandbox_id'] == owned and manifest['sandbox_id'] == owned,
            'captured source identity mismatch')
    validate_capture_metadata(plan, manifest,
        {name: private(metadata / (name + '.json')) for name in ['cubebox', 'storage']})
    output.mkdir(mode=0o700)
    files = {'cubebox.json': metadata / 'cubebox.json',
             'storage.json': metadata / 'storage.json',
             'plan.json': metadata / 'plan.json',
             'rescue-input.json': inputs / 'rescue-input.json',
             'fence.json': Path(args.capture_fence), 'export-report.json': export / 'export-report.json'}
    capture_fence = private(args.capture_fence)
    if capture_fence['current_boot_id'] != boot:
        require(args.post_capture_reboot is not None, 'explicit post-capture reboot receipt required')
        files['post-capture-reboot.json'] = Path(args.post_capture_reboot)
    else:
        require(args.post_capture_reboot is None, 'unexpected post-capture reboot receipt')
    if exported.get('explicit_repair_receipt_sha256'):
        require(digest(export / 'repair-receipt.json') == exported['explicit_repair_receipt_sha256'],
                'explicit repair receipt mismatch')
        for name in ['repair-receipt.json', 'repair-fsck.log', 'verify-fsck.log']:
            files[name] = export / name
    hashes = {}
    for name, source in files.items():
        if not name.endswith('.log'):
            private(source)
        else:
            require(source.is_file() and not source.is_symlink() and source.stat().st_uid == 0 and
                    source.stat().st_mode & 0o077 == 0 and source.stat().st_size <= 8 << 20,
                    'private bounded repair log required')
        target = output / name
        with target.open('xb') as dest, source.open('rb') as original:
            os.fchmod(dest.fileno(), 0o600)
            shutil.copyfileobj(original, dest)
            dest.flush(); os.fsync(dest.fileno())
        hashes[name] = digest(target)
    write(output / 'recovery-evidence.json', {
        'purpose': 'OWNED_CURRENT_DISK_RETAINED_PROVIDER', 'old_provider_id': owned,
        'fixture': old['fixture'], 'worker_machine_id': old['worker_machine_id'],
        'previous_boot_id': old['worker_boot_id'], 'current_boot_id': boot,
        'current_archive_sha256': exported['archive_sha256'], 'files_sha256': hashes,
        'no_task_verified': True, 'no_owned_vmm_or_disk_fd_verified': True,
        'management_fenced': True, 'checked_at': fence['checked_at'], 'expires_at': fence['expires_at'],
        'execution_fence_sha256': digest(args.execution_fence)})
    write(output / 'handoff.json', {'purpose': 'DISPOSABLE_CURRENT_DISK_JOURNAL',
        'fixture': old['fixture'], 'old_provider_id': owned, 'worker_machine_id': old['worker_machine_id'],
        'current_archive_sha256': exported['archive_sha256'],
        'recovery_evidence_sha256': digest(output / 'recovery-evidence.json'), 'expires_at': fence['expires_at']})
    write(output / 'native-archive.json', {'retained_home_tar_path': str(export / 'home.tar'),
                                        'retained_current_disk_path': str(inputs / 'current.ext4')})
    stage_converted(export / 'converted', output)
    write(output / 'prepared-command.json', {
        'cwd': str(Path(args.binary).parent / 'cmd/operator-journal-acceptance'),
        'argv': journal_argv(args.binary, old_dir, output),
        'binary_sha256': args.binary_sha256, 'executed': False,
        'required_limits': {'CPUQuota': '200%', 'MemoryMax': '2G', 'TasksMax': 128, 'RuntimeMaxSec': '21min'}})
    fd = os.open(output, os.O_DIRECTORY); os.fsync(fd); os.close(fd)
    print(json.dumps({'prepared': True, 'old_provider_id': owned, 'guest_mutations': False,
                      'services_changed': False, 'executed': False}))

if __name__ == '__main__':
    main()
