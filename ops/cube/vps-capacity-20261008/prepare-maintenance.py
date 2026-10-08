"""Install tested worker-scoped operator helpers and generate a fresh VPS plan.

Does not stop the worker, change routing, or contact the B200. Keep the current
40 GiB supervisor installed until its normal clean-stop receipt exists.
"""
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sys
import sqlite3
import subprocess
import urllib.request

os.umask(0o077)
ROOT = Path('/opt/baarcha/operations/vps-50-profiles-20261008')
SRC = ROOT / 'source'
BEFORE = ROOT / 'maintenance-before'
locks = []
for path in ('/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock',
             '/run/lock/cube-operator-acceptance.lock', '/opt/baarcha-bench/cube-workload-operator.lock'):
    fd = open(path, 'a'); fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB); locks.append(fd)
if "--plan-only" not in sys.argv:
    BEFORE.mkdir(mode=0o700)
    def install(source, target, mode):
        target = Path(target)
        if target.exists(): shutil.copy2(target, BEFORE / target.name)
        pending = target.with_name(target.name + '.capacity-pending')
        shutil.copyfile(source, pending); pending.chmod(mode); os.replace(pending, target)
    
    assert (SRC / 'lifecycle-tests-final.log').read_text().find('FAIL') == -1
    for binary in ('stop', 'start'):
        install(SRC / ('worker-' + binary + '-candidate'),
                '/usr/local/libexec/baarcha-cube-worker-' + binary, 0o755)
    for name in ('maintenance', 'boot_transition'):
        install(SRC / 'ops/cube/worker-lifecycle' / (name + '.py'),
                '/usr/local/libexec/baarcha-cube-' + name.replace('_', '-') + '.py', 0o755)
    shutil.copytree(SRC / 'control-plane/migrations', ROOT / 'operator-migrations')
    

cp = json.loads(subprocess.check_output(['docker', 'inspect', 'src-sandboxd-1']))[0]
assert cp['State']['Running']
env = dict(x.split('=', 1) for x in cp['Config']['Env'])
stop_path = Path('/etc/baarcha-cube/worker-stop.json')
stop = json.loads(stop_path.read_bytes())
assert stop['controller_id'] == cp['Id']
if "--plan-only" not in sys.argv:
    shutil.copy2(stop_path, BEFORE / stop_path.name)
    stop.update(worker_id='vps', master_url='http://10.254.240.1:18089',
                admission=json.loads(env['SANDBOXD_CUBE_ADMISSION']),
                migrations=str(ROOT / 'operator-migrations'))
    temp = ROOT / 'stop-wanted.PRIVATE.json'; temp.write_text(json.dumps(stop)); temp.chmod(0o600)
    install(temp, stop_path, 0o600)
    

spec = importlib.util.spec_from_file_location('planned', '/usr/local/libexec/baarcha-cube-planned.py')
p = importlib.util.module_from_spec(spec); spec.loader.exec_module(p)
status = json.loads((p.m.ROOT / 'lifecycle-status.json').read_bytes())
e = {k: stop[k] for k in ('controller_id', 'worker_machine_id', 'worker_boot_id', 'data_uuid', 'qemu_pid', 'qemu_start_time')}
e.update(controller_image=cp['Image'], outer_machine_id=Path('/etc/machine-id').read_text().strip(),
         outer_boot_id=Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
         supervisor_pid=status['supervisor_pid'], supervisor_start_time=p.x.ticks(status['supervisor_pid']))
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro', uri=True) as db:
    rows = db.execute("SELECT b.sandbox_id,s.app_id,b.runtime_id,b.template_id,b.config_revision FROM runtime_binding b JOIN sandbox s ON s.id=b.sandbox_id JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE b.provider='cube' AND a.worker_id='vps' AND a.state<>'deleted' ORDER BY b.sandbox_id").fetchall()
bindings = [dict(zip(('sandbox_id','app_id','runtime_id','template_id','config_revision'), row)) for row in rows]
assert len(bindings) == 7
online = json.load(urllib.request.urlopen('http://127.0.0.1:2019/config/'))
old = json.loads(Path('/opt/baarcha-cube/motion-release-20260926/release-plan.PRIVATE.json').read_bytes())
proxy = json.loads(subprocess.check_output(['docker', 'inspect', old['fence']['writer_containers'][0]]))[0]
worker = int(subprocess.check_output(['systemctl', 'show', 'baarcha-motion-worker.service', '-p', 'MainPID', '--value']))
plan = {'version':1, 'kind':p.KIND, 'expected':e,
        'files':{str(f):p.m.file_digest(f) for f in p.FILES}, 'bindings':bindings,
        'routing':{'server':'srv0', 'online_sha256':p.b.sha(json.dumps(online,sort_keys=True,separators=(',',':')).encode()),
                   'platform_hosts':['baarcha.tn','www.baarcha.tn'],
                   'preview_hosts':['*.preview.baarcha.tn','*.preview.65.108.225.153.sslip.io'],
                   'motion_hosts':['s-01M3CKN99ZF90BEEA4DS66YAQV-3000.preview.65.108.225.153.sslip.io'],
                   'unaffected_hosts':['bp.tn','www.bp.tn','hh1.dovisual.com'],
                   'preview_probe_hosts':['s-'+bindings[0]['sandbox_id']+'-3000.preview.baarcha.tn']},
        'motion':{'proxy_id':proxy['Id'],'proxy_image':proxy['Image'], 'worker_pid':worker,'worker_start_time':p.x.ticks(worker)},
        'provider_terminal_counts':{}}
host = p.Host(plan, ROOT, [], lambda *a:None)
plan['provider_terminal_counts'] = host.provider_counts()
p.validate_plan(plan)
(ROOT / 'vps-resize-plan.PRIVATE.json').write_bytes(p.b.encoded(plan))
print(json.dumps({'helpers_installed':True,'bindings':len(bindings),'worker_restarted':False}))
