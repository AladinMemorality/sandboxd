#!/usr/bin/env python3
"""One isolated B200 microVM before production fleet enrollment; no customer data.

Requires the live controller to remain VPS-only and explicitly pinned. Holds the
operator locks, requires an empty B200 inventory and fresh storage measurements,
and re-cordons B200 on exit. Private write-ahead evidence prevents blind replay.
This is native worker acceptance, not the fifty-sandbox production load test.
"""
import fcntl
import http.client
import io
import json
import os
from pathlib import Path
import secrets
import select
import subprocess
import sys
import time
import zipfile
from urllib.parse import urlsplit

NODE = '10.254.240.2'
MASTER = 'http://10.254.240.1:18089'
OPS = 'http://10.254.240.1:13010'
API = 'http://127.0.0.1:20300'
TEMPLATE = 'tpl-5abec4cb4fcc41cc8e611f69'


def request(origin, path, method='GET', body=None, headers=None, timeout=45):
    u = urlsplit(origin)
    c = http.client.HTTPConnection(u.hostname, u.port, timeout=timeout)
    try:
        c.request(method, path, body if isinstance(body, bytes) else None if body is None else json.dumps(body),
                  {'Content-Type': 'application/json', **(headers or {})})
        r = c.getresponse(); raw = r.read(2*1024**2+1)
        assert len(raw) <= 2*1024**2
        return r.status, raw
    finally:
        c.close()


def native(origin, path, method='GET'):
    status, raw = request(origin, path, method)
    assert status == 200
    return json.loads(raw)


def empty():
    v = native(MASTER, '/cube/sandbox/inventory?host_id='+NODE)
    # Cube's total counts healthy nodes, not sandboxes; size is nodes scanned.
    assert v['ret']['ret_code'] == 200 and v['size'] == 1 and v['data'] == []


def main():
    assert os.geteuid() == 0
    os.umask(0o077)
    job = Path(sys.argv[1])
    assert job.parent == Path('/opt/baarcha-bench/cube-fleet-20260927')
    job.mkdir(mode=0o700)  # A previous create intent requires explicit recovery.
    locks = []
    for name in ('/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock',
                 '/run/lock/cube-operator-acceptance.lock', '/opt/baarcha-bench/cube-workload-operator.lock'):
        fd = os.open(name, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB); locks.append(fd)
    cp = json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
    env = dict(s.split('=',1) for s in cp['Config']['Env'])
    assert cp['State']['Running'] and not env.get('SANDBOXD_CUBE_FLEET')
    assert json.loads(env['SANDBOXD_CUBE_ADMISSION'])['node_id'] == '10.0.2.15'
    guard = json.loads(Path('/etc/baarcha-cube/storage-guard-b200.json').read_text())
    observation = json.loads(Path(guard['observation_path']).read_text())
    for a,b in [('observer_id','observer_id'),('outer_boot_id','outer_boot_id'),('worker_machine_id','worker_machine_id'),
                ('worker_boot_id','expected_boot_id'),('inner_fs_uuid','inner_fs_uuid'),('outer_fs_uuid','outer_fs_uuid')]:
        assert observation[a] == guard[b]
    assert observation['outer_boot_id'] == Path('/proc/sys/kernel/random/boot_id').read_text().strip()
    now = time.clock_gettime_ns(time.CLOCK_BOOTTIME)
    assert 0 < observation['started_boottime_ns'] <= observation['completed_boottime_ns'] <= now
    assert now-observation['started_boottime_ns'] < 30_000_000_000
    assert min(observation['inner_free_bytes'],observation['outer_free_bytes']) > 96*1024**3
    node = native(OPS, '/internal/v1/nodes/'+NODE)
    assert node['Healthy'] and node['SchedulingDisabled'] and TEMPLATE in node['LocalTemplates']
    empty()
    prefix = 'fleet-canary-'+secrets.token_hex(12)
    token = secrets.token_hex(32)
    body = {'templateID':TEMPLATE,'timeout':600,'distributionScope':[NODE],
            'lifecycle':{'onTimeout':'pause','autoResume':False},'allow_internet_access':False,
            'network':{'allowPublicTraffic':False,'allowOut':[],'denyOut':['0.0.0.0/0']},
            'envVars':{'RUNTIMED_HTTP_ADDR':':3031','RUNTIMED_HTTP_TOKEN':token},
            'metadata':{'sandboxd_id':prefix,'sandboxd_app_id':prefix,'fixture':'b200-native-canary'}}
    headers = {'X-API-Key':env['SANDBOXD_CUBE_API_KEY']}
    report = {'complete':False,'production_fleet_accepted':False,'fixture':prefix}
    def save(name, value):
        (job/name).write_text(json.dumps(value))
    save('intent.PRIVATE.json',body)
    vm = None
    channel = None
    try:
        native(OPS, '/internal/v1/nodes/'+NODE+'/isolation', 'DELETE')
        # Master's node cache polls CubeOps every three seconds.
        time.sleep(5)
        assert not native(OPS, '/internal/v1/nodes/'+NODE)['SchedulingDisabled']
        began = time.monotonic()
        status, raw = request(API, '/sandboxes', 'POST', body, headers)
        (job/'create-response.PRIVATE.json').write_bytes(raw)
        assert status == 201, 'native create rejected; retain private response'
        vm = json.loads(raw); ident = vm['sandboxID']
        assert ident and all(c.isalnum() or c in '-_' for c in ident)
        report.update(runtime_id=ident,create_seconds=time.monotonic()-began)
        info = native(MASTER, '/cube/sandbox/info?sandbox_id='+ident+'&instance_type=cubebox')
        assert info['ret']['ret_code'] == 200 and len(info['data']) == 1
        assert info['data'][0]['sandbox_id'] == ident and info['data'][0]['host_id'] == NODE
        traffic = {'cube-traffic-access-token':vm['trafficAccessToken']}
        end = time.monotonic()+60
        while True:
            try:
                status, raw = request('http://'+NODE+':28080', '/status', headers={**traffic,
                    'Host':'3031-'+ident+'.cube.app','Authorization':'Bearer '+token},timeout=8)
                assert status == 200
                save('supervisor-status.PRIVATE.json',json.loads(raw));break
            except (OSError,AssertionError):
                if time.monotonic() >= end:raise
                time.sleep(1)
        report['supervisor_ready_seconds'] = time.monotonic()-began
        supervisor = {**traffic,'Host':'3031-'+ident+'.cube.app','Authorization':'Bearer '+token}
        status, _ = request('http://'+NODE+':28080','/workspace/quiesce','POST',headers=supervisor)
        assert status == 200
        archive = io.BytesIO()
        source = "const fs=require('fs'); const http=require('http'); const body='<html>"+prefix+" gpu='+fs.readdirSync('/dev').some(x=>x.startsWith('nvidia'))+'</html>'; http.createServer((q,r)=>{r.setHeader('Content-Type','text/html');r.end(body)}).listen(3000,'0.0.0.0'); setTimeout(()=>process.exit(0),300000);"
        with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as z:
            for name, data in [('sandbox.yaml','version: 1\nweb:\n  command: node canary.cjs\n  port: 3000\n  health_path: /\nbuild:\n  command: ""\n'),('canary.cjs',source)]:
                info = zipfile.ZipInfo(name);info.external_attr = 0o100644 << 16
                z.writestr(info,data)
        status, _ = request('http://'+NODE+':28080','/import/private-workspace','PUT',archive.getvalue(),
                            {**supervisor,'Content-Type':'application/zip'})
        assert status == 200
        channel = subprocess.Popen(['/opt/baarcha-bench/cube-fleet-20260927/admission-test/cube-fleet-canary-channel',str(job)],
                                   stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        readable, _, _ = select.select([channel.stdout],[],[],45)
        assert readable and channel.stdout.readline() == b'CANARY_CHANNEL_READY\n'
        end = time.monotonic()+45
        while True:
            status, raw = request('http://'+NODE+':28080', '/',headers={**traffic,'Host':'3000-'+ident+'.cube.app'},timeout=8)
            if status == 200 and prefix.encode() in raw and b'gpu=false' in raw:break
            assert time.monotonic() < end, 'application preview unavailable'
            time.sleep(1)
        report.update(preview_status=status,preview_ready_seconds=time.monotonic()-began,complete=True)
    finally:
        # Always close admission first, including unknown create outcomes.
        native(OPS, '/internal/v1/nodes/'+NODE+'/isolation', 'PUT')
        if channel is not None:
            channel.stdin.close()
            try:
                channel.wait(timeout=10)
            except subprocess.TimeoutExpired:
                channel.terminate();channel.wait(timeout=10)
        if vm is not None:
            status, raw = request(API,'/sandboxes/'+vm['sandboxID'],'GET',headers=headers)
            assert status == 200 and json.loads(raw)['metadata']['fixture'] == 'b200-native-canary'
            assert json.loads(raw)['metadata']['sandboxd_id'] == prefix
            status, _ = request(API,'/sandboxes/'+vm['sandboxID'],'DELETE',headers=headers)
            assert status == 204, 'owned cleanup requires review'
            empty();report['cleanup_verified'] = True
        save('result.json',report)
        print(json.dumps(report),flush=True)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        raise SystemExit('B200 native canary needs review: '+type(error).__name__)
