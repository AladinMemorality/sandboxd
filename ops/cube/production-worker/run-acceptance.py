#!/usr/bin/env python3
"""Run the reviewed seven-preset fixture only after the coordinated security run.

No overlapping guests/builds. Stops on the first failure. Root owns the compiled
fixture/source under acceptance/; this runner does not edit or rebuild it.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess

base = Path('/root/cube-production')
if subprocess.check_output(['hostname'], text=True).strip() != 'baarcha-cube-worker-01':
    raise RuntimeError('fresh worker required')
if not (base / 'security-fixture-complete').exists():
    raise RuntimeError('coordinator has not confirmed security guest cleanup')
binary = base / 'acceptance/cube-worker-acceptance'
expected = 'ff785b3b35984b1c9c306c31d8794733013a13b07902a70dfb4b16fa8b6b3d3f'
if hashlib.sha256(binary.read_bytes()).hexdigest() != expected:
    raise RuntimeError('reviewed acceptance binary hash mismatch')
manifest = json.loads((base / 'verified-templates.json').read_text())
if len(manifest) != 8:
    raise RuntimeError('all8 verified templates required')
reports = []
for item in manifest:
    preset = item['preset']
    if preset == 'node-postgres':
        continue
    inventory = subprocess.check_output(['cubemastercli', '-a', '127.0.0.1', 'list', '--all', '--wide'], text=True)
    if 'SANDBOX_COUNT    0' not in inventory:
        raise RuntimeError('unexpected guest; do not overlap fixtures or clean other owners')
    stat = os.statvfs('/data')
    if stat.f_bavail * stat.f_frsize < 80 * 1024**3:
        raise RuntimeError('less than80GiB data free')
    report = base / 'acceptance' / (preset + '.json')
    if report.exists():
        raise RuntimeError('prior report exists; review before rerunning')
    log = base / 'acceptance' / (preset + '.txt')
    with log.open('wb') as stream:
        result = subprocess.run([str(binary), 'preset', item['template_id'], preset, str(report)],
                                stdout=stream, stderr=stream, timeout=600)
    if result.returncode != 0:
        raise RuntimeError('functional fixture failed: ' + preset + '; inspect private log and owned guest cleanup')
    data = json.loads(report.read_text())
    if data.get('delete_verified_http404') is not True:
        raise RuntimeError('fixture cleanup not proven')
    if data.get('cpu_count') != 2 or data.get('memory_mb') != 2048:
        raise RuntimeError('resource mismatch')
    if 'pause_resume_same_supervisor_and_process' not in data.get('checks', []):
        raise RuntimeError('same-process resume not proven')
    reports.append(data)
    (base / 'acceptance/results.json').write_text(json.dumps({'binary_sha256': expected, 'results': reports}, indent=2) + '\n')
    print(json.dumps({'preset': preset, 'status': 'PASS', 'timings': data['timings'], 'deleted': True}), flush=True)
print('PASS all7 functional presets; PostgreSQL full API test remains separate', flush=True)
