#!/usr/bin/env python3
"""Render NEW review artifacts only. Never install, start, stop or reconfigure services."""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import observe

UNVERIFIED = 'UNVERIFIED_POST_CYCLE_BOOT_DO_NOT_INSTALL'
MIGRATIONS_TARGET = '/opt/baarcha-cube/coordinator-storage-guard-20260925/migrations'


def render(pins, boot, admission, stop, templates):
    # The receipt is a private operator attestation bound to retained actual
    # readiness evidence. This renderer does not execute or invent that proof.
    if set(boot) != {'version', 'verified', 'outer_boot_id', 'worker_machine_id', 'worker_boot_id', 'data_uuid', 'evidence_sha256', 'qemu_pid', 'qemu_start_time', 'controller_id'} or boot['version'] != 1 or boot['verified'] is not True or not re.fullmatch('[0-9a-f]{64}', boot['evidence_sha256']):
        raise observe.Invalid('verified post-cycle boot receipt required')
    if type(boot['qemu_pid']) is not int or boot['qemu_pid'] < 2 or not isinstance(boot['qemu_start_time'], str) or not re.fullmatch('[1-9][0-9]*', boot['qemu_start_time']) or not re.fullmatch('[0-9a-f]{64}', boot['controller_id']):
        raise observe.Invalid('exact current QEMU and controller generations required')
    if not observe.UUID.fullmatch(boot['worker_boot_id']):
        raise observe.Invalid('unverified worker boot placeholder')
    if boot['outer_boot_id'] != pins['outer_boot_id'] or boot['worker_machine_id'] != pins['worker_machine_id'] or boot['data_uuid'] != pins['inner_fs_uuid']:
        raise observe.Invalid('post-cycle identity differs from reviewed pins')
    if pins['expected_boot_id'] not in (UNVERIFIED, boot['worker_boot_id']):
        raise observe.Invalid('old worker boot must not silently change')
    guard = dict(pins, expected_boot_id=boot['worker_boot_id'])
    observe.validate_config(guard)
    if admission.get('max_active') != 4 or admission.get('cpu_count') != 2 or admission.get('memory_mb') != 2048 or not admission.get('templates'):
        raise observe.Invalid('reviewed four-slot profile required')
    if set(admission) - {'max_active', 'cpu_count', 'memory_mb', 'templates', 'storage_guard', 'writable_disk_mb'}:
        raise observe.Invalid('unexpected admission fields')
    rows = templates.get('templates', [])
    if templates.get('verified') != 8 or len(rows) != 8:
        raise observe.Invalid('independent eight-template capacity proof required')
    actual = {}
    for row in rows:
        if row.get('ready') is not True or row.get('cpu') != 2 or row.get('memory_mb') != 2048 or row.get('rootfs_writable_mb') != 10240 or row.get('single_rootfs_volume') is not True or row.get('deny_all_egress') is not True or row['template_id'] in actual:
            raise observe.Invalid('template does not meet hard disk and resource contract')
        actual[row['template_id']] = {'cpu_count': 2, 'memory_mb': 2048}
    if admission['templates'] != actual:
        raise observe.Invalid('admission template set differs from actual capacity proof')
    def profile(value):
        return {k: v for k, v in value.items() if k not in ('storage_guard', 'writable_disk_mb')}
    if profile(stop['admission']) != profile(admission) or stop['api_url'] != 'http://127.0.0.1:20300' or stop['worker_machine_id'] != guard['worker_machine_id'] or stop['data_uuid'] != guard['inner_fs_uuid']:
        raise observe.Invalid('stop coordinator profile or identity differs')
    for old in (admission.get('storage_guard'), stop['admission'].get('storage_guard')):
        if old is not None and {k:v for k,v in old.items() if k != 'expected_boot_id'} != {k:v for k,v in guard.items() if k != 'expected_boot_id'}:
            raise observe.Invalid('existing observer identity must not be replaced')
    new_admission = dict(admission, storage_guard=guard, writable_disk_mb=10240)
    new_stop = copy.deepcopy(stop)
    new_stop.update(admission=new_admission, worker_boot_id=boot['worker_boot_id'], migrations=MIGRATIONS_TARGET, qemu_pid=boot['qemu_pid'], qemu_start_time=boot['qemu_start_time'], controller_id=boot['controller_id'])
    return guard, new_admission, new_stop


def compose_override(admission):
    value = json.dumps(json.dumps(admission, separators=(',', ':')))
    return '''# Prepared candidate; install only after reviewed observer/config enrollment.
services:
  sandboxd:
    environment:
      SANDBOXD_CUBE_ADMISSION: ''' + value + '''
    volumes:
      - type: bind
        source: /run/sandboxd-cube-storage
        target: /run/sandboxd-cube-storage
        read_only: true
        bind:
          create_host_path: false
'''


def main():
    p=argparse.ArgumentParser(description=__doc__)
    for name in ('pins', 'boot-receipt', 'admission', 'worker-stop', 'template-proof', 'out'):
        p.add_argument('--'+name, required=True, type=Path)
    args=p.parse_args()
    if os.geteuid()!=0:
        raise observe.Invalid('private root preparation required')
    pins,boot,admission,stop,templates=[observe.trusted_read(x) for x in (args.pins,args.boot_receipt,args.admission,args.worker_stop,args.template_proof)]
    guard,new_admission,new_stop=render(pins,boot,admission,stop,templates)
    if not args.out.is_absolute() or args.out.parent.resolve()!=args.out.parent:
        raise observe.Invalid('canonical new private output directory required')
    args.out.mkdir(mode=0o700,exist_ok=False)
    files={
        'storage-guard.json':guard,
        'admission.json':new_admission,
        'worker-stop.json':new_stop,
    }
    manifest={'version':1,'installed':False,'boot_evidence_sha256':boot['evidence_sha256'],'files':{}}
    for name,value in files.items():
        raw=(json.dumps(value,indent=2)+'\n').encode();path=args.out/name
        fd=os.open(path,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o600)
        with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
        manifest['files'][name]=hashlib.sha256(raw).hexdigest()
    raw=compose_override(new_admission).encode();path=args.out/'docker-compose.storage-guard.yml'
    fd=os.open(path,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
    manifest['files'][path.name]=hashlib.sha256(raw).hexdigest()
    observe.atomic_json(args.out/'review-manifest.json',manifest)
    # No credentials, rendered content or root-private source paths on stdout.
    print('Prepared new private review bundle; nothing installed or started')

if __name__=='__main__':
    try:main()
    except (observe.Invalid,OSError,ValueError,KeyError,TypeError):
        raise SystemExit('Storage enrollment preparation refused; no live configuration changed')
