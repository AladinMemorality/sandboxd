#!/usr/bin/env python3
"""Sequential trusted template builds on the fresh worker. No tenant creation.

Run inside worker-01 after its local registry manifest is verified. A failed or
unfinished previous job halts this script for investigation; it is never deleted.
"""
import json
import os
from pathlib import Path
import subprocess

BASE = Path('/root/cube-production')

def cli(*args):
    return subprocess.check_output(['cubemastercli', *args], text=True)

def main():
    if subprocess.check_output(['hostname'], text=True).strip() != 'baarcha-cube-worker-01':
        raise RuntimeError('fresh worker hostname required')
    manifest = json.loads((BASE / 'registry-template-images.json').read_text())
    if len(manifest) != 8:
        raise RuntimeError('exactly8 verified images required')
    destination = BASE / 'registered-templates.json'
    results = json.loads(destination.read_text()) if destination.exists() else []
    for item in manifest:
        preset = item['preset']
        if any(row['preset'] == preset for row in results):
            continue
        stat = os.statvfs('/data')
        before = stat.f_bavail * stat.f_frsize
        if before < 80 * 1024**3:
            raise RuntimeError('less than80GiB actual data free; no new build')
        inventory = cli('-a', '127.0.0.1', 'list', '--all', '--wide')
        if 'SANDBOX_COUNT    0' not in inventory:
            raise RuntimeError('unexpected existing guest/build; inspect before proceeding')
        jobfile = BASE / (preset + '-production-template-job.json')
        if jobfile.exists():
            raise RuntimeError('prior build record exists; inspect/reconcile it explicitly')
        created = json.loads(cli('tpl', 'create-from-image', '--image', item['digest'],
            '--alias', 'baarcha-prod-' + preset + '-20260924-v4',
            '--expose-port', '3000', '--expose-port', '3001', '--expose-port', '3031',
            '--probe', '49983', '--probe-path', '/health', '--cpu', '2000', '--memory', '2048',
            '--writable-layer-size', '10Gi', '--with-cube-ca=false', '--deny-out-cidr', '0.0.0.0/0',
            '--json', '--detach'))
        jobfile.write_text(json.dumps(created, indent=2) + '\n')
        jobfile.chmod(0o600)
        job = created['job']
        with (BASE / (preset + '-production-template-watch.log')).open('wb') as log:
            subprocess.run(['cubemastercli', 'tpl', 'watch', '--job-id', job['job_id'], '--json'],
                           stdout=log, stderr=log, check=True, timeout=900)
        status = json.loads(cli('tpl', 'status', '--job-id', job['job_id'], '--json'))
        if status['job']['status'] != 'READY':
            raise RuntimeError('template not ready')
        stat = os.statvfs('/data')
        record = dict(item, template_id=job['template_id'], job_id=job['job_id'],
                      state='READY', writable_layer='10Gi', exposed_ports=[3000, 3001, 3031],
                      probe_port=49983, probe_path='/health', with_cube_ca=False,
                      deny_out=['0.0.0.0/0'], free_bytes_before=before,
                      free_bytes_after=stat.f_bavail * stat.f_frsize)
        results.append(record)
        destination.write_text(json.dumps(results, indent=2) + '\n')
        print(json.dumps(record), flush=True)

if __name__ == '__main__':
    main()
