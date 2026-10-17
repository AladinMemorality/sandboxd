#!/usr/bin/env python3
"""Stage uniquely named candidate templates on the active VPS worker only.

Run on the VPS after the candidate image receipt exists. This does not change default
templates, customer bindings, controller configuration or platform services.
An existing create intent without a READY receipt requires manual reconciliation.
"""
import fcntl
import json
from pathlib import Path
import shlex
import subprocess
import sys

ROOT = Path('/opt/baarcha-bench/nextjs-postgres-20261017')
SSH = ['ssh', '-i', '/opt/baarcha-cube/worker-01/operator-key', '-p', '20222',
       '-oBatchMode=yes', '-oConnectTimeout=5', '-oStrictHostKeyChecking=yes',
       '-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts', 'root@127.0.0.1']


def remote(args, **kwargs):
    return subprocess.check_output(SSH + [shlex.join(args)], **kwargs)


def main():
    presets = sys.argv[1:] or ['nextjs-postgres']
    if not presets or set(presets) - {'nextjs-postgres'}:
        raise ValueError('Unknown preset')
    receipts = [json.loads((ROOT / (preset + '-image.json')).read_text()) for preset in presets]
    with open('/run/lock/cube-operator-acceptance.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        facts = json.loads(remote(['python3', '-c', 'import os,json;print(json.dumps({"free":os.statvfs("/data").f_bavail*os.statvfs("/data").f_frsize,"memory":int(next(x.split()[1] for x in open("/proc/meminfo") if x.startswith("MemAvailable:")))*1024}))']))
        if facts['free'] < 50 * 1024**3 or facts['memory'] < 8 * 1024**3:
            raise RuntimeError('Insufficient worker headroom for a candidate build')
        for receipt in receipts:
            preset, tag = receipt['preset'], receipt['tag']
            if (ROOT / (preset + '-ready.json')).exists():
                continue
            intent = ROOT / (preset + '-create-intent.json')
            if intent.exists():
                raise RuntimeError('Prior create intent exists; reconcile by alias/job before continuing')
            actual = subprocess.check_output(['docker', 'image', 'inspect', tag, '--format', '{{.Id}}'], text=True).strip()
            if actual != receipt['image']:
                raise RuntimeError('Candidate image changed')
            with (ROOT / (preset + '-transfer.log')).open('wb') as log:
                saved = subprocess.Popen(['docker', 'save', tag], stdout=subprocess.PIPE, stderr=log)
                try:
                    loaded = subprocess.run(SSH + ['docker load'], stdin=saved.stdout, stdout=log, stderr=log)
                finally:
                    saved.stdout.close()
                if saved.wait() != 0 or loaded.returncode != 0:
                    raise RuntimeError('Candidate image transfer failed')
            target = '127.0.0.1:5000/baarcha/' + preset + ':nextjs-postgres-20261017'
            remote(['docker', 'tag', tag, target])
            (ROOT / (preset + '-push.log')).write_bytes(remote(['docker', 'push', target], stderr=subprocess.STDOUT))
            digests = json.loads(remote(['docker', 'image', 'inspect', target, '--format', '{{json .RepoDigests}}']))
            digest = next(value for value in digests if value.startswith(target.split(':nextjs-postgres')[0] + '@sha256:'))
            args = ['cubemastercli', 'tpl', 'create-from-image', '--image', digest,
                    '--alias', 'baarcha-' + preset + '-20261017', '--node', '10.0.2.15',
                    '--expose-port', '3000', '--expose-port', '3001', '--expose-port', '3031',
                    '--probe', '49983', '--probe-path', '/health', '--cpu', '2000', '--memory', '4096',
                    '--writable-layer-size', '10Gi', '--with-cube-ca=false', '--deny-out-cidr', '0.0.0.0/0', '--json', '--detach']
            with intent.open('x') as stream:
                json.dump({'image': digest, 'args': args, 'headroom': facts}, stream)
                stream.flush()
                import os
                os.fsync(stream.fileno())
            created = json.loads(remote(args))
            (ROOT / (preset + '-job.json')).write_text(json.dumps(created, indent=2))
            job = created['job']
            (ROOT / (preset + '-watch.log')).write_bytes(remote(['cubemastercli', 'tpl', 'watch', '--job-id', job['job_id'], '--json'], timeout=900))
            status = json.loads(remote(['cubemastercli', 'tpl', 'status', '--job-id', job['job_id'], '--json']))
            if status['job']['status'] != 'READY':
                raise RuntimeError('Candidate template not READY')
            result = {**receipt, 'template_id': job['template_id'], 'job_id': job['job_id'], 'registration': status, 'ready_nodes': ['10.0.2.15']}
            (ROOT / (preset + '-ready.json')).write_text(json.dumps(result, indent=2))
            print(json.dumps({'preset': preset, 'template_id': job['template_id'], 'status': 'READY'}), flush=True)


if __name__ == '__main__': main()
