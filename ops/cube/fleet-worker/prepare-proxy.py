#!/usr/bin/env python3
"""Render B200's private proxy from the reviewed production proxy configuration.

Inputs contain Redis/admin credentials and must be transferred privately into
the worker. No credential is printed or copied into a sandbox.
"""
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path('/root/cube-fleet-install/proxy-inputs')
DEST = Path('/usr/local/services/cubetoolbox/cubeproxy-fleet')
IMAGE = 'cube-sandbox-int.tencentcloudcr.com/cube-sandbox/cube-proxy@sha256:b5a196be3698d820c1ae70a3a7a9fd0439badd43e74a2c4954255f7dc69f86dc'


def main():
    assert os.geteuid() == 0
    assert subprocess.check_output(['hostname'], text=True).strip() == 'baarcha-cube-worker-b200-01'
    os.umask(0o077)
    DEST.mkdir(mode=0o700, exist_ok=False)
    config = (ROOT / 'global.conf').read_text()
    def value(name):
        m = re.search(r'^set \$' + name + r' "([^"\r\n]*)";$', config, re.M)
        assert m, 'missing private proxy setting'
        return m.group(1)
    password, token = value('redis_pd'), value('cube_admin_token')
    assert password, 'production Redis credential required'
    # Admin binds only to the authenticated coordinator WireGuard peer. Preserve
    # the cluster's admin token (including its existing empty-token mode).
    for name, new in {'redis_ip':'10.254.240.1', 'redis_port':'16379',
                      'cube_proxy_host_ip':'10.254.240.2',
                      'cube_sidecar_addr':'10.254.240.1:18083'}.items():
        config, n = re.subn(r'^set \$' + name + r' "[^"\r\n]*";$',
                            'set $' + name + ' "' + new + '";', config, flags=re.M)
        assert n == 1
    config = config.replace('set $timeout_min 500;', 'set $timeout_min 2000;').replace('set $timeout_max 700;', 'set $timeout_max 3000;')
    (DEST / 'global.conf').write_text(config)
    nginx = (ROOT / 'nginx.conf').read_text()
    # Vite and modern application servers use these current JavaScript types.
    # Compress at the worker before bytes cross the management network.
    matches=list(re.finditer(r'(?m)^\s*gzip_types\s+([^;]+);',nginx));assert len(matches)==1
    match=matches[0];types=match.group(1).split()
    for content_type in ('application/javascript','text/javascript'):
        if content_type not in types:types.append(content_type)
    nginx=nginx[:match.start(1)]+' '.join(types)+nginx[match.end(1):]
    for old, new in [('worker_processes auto;', 'worker_processes 4;'),
                     ('listen 80 reuseport;', 'listen 10.254.240.2:28080 reuseport;'),
                     ('listen 443 ssl reuseport;', 'listen 10.254.240.2:28443 ssl reuseport;'),
                     ('listen 9090 http2 reuseport;', 'listen 10.254.240.2:29090 http2 reuseport;'),
                     ('listen 10.0.2.15:8082;', 'listen 10.254.240.2:28082;')]:
        assert nginx.count(old) == 1, 'unexpected production nginx configuration'
        nginx = nginx.replace(old, new)
    (DEST / 'nginx.conf').write_text(nginx)
    certs = DEST / 'certs'
    certs.mkdir(mode=0o700)
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '365',
                    '-subj', '/CN=cube-fleet.internal', '-keyout', str(certs/'cube.app+3-key.pem'),
                    '-out', str(certs/'cube.app+3.pem')], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    env = {'CUBE_PROXY_REGISTRY_ENABLE':'1', 'CUBE_PROXY_ID':'b200-01',
           'CUBE_PROXY_ADMIN_URL':'http://10.254.240.2:28082',
           'CUBE_PROXY_RESUME_URL':'http://10.254.240.2:28082',
           'CUBE_PROXY_NODE_IP':'10.254.240.2',
           'CUBE_PROXY_HEARTBEAT_INTERVAL_MS':'5000',
           'CUBE_PROXY_REGISTRY_REDIS_HOST':'10.254.240.1',
           'CUBE_PROXY_REGISTRY_REDIS_PORT':'16379',
           'CUBE_PROXY_REGISTRY_REDIS_PASSWORD':password,
           'CUBE_PROXY_REGISTRY_REDIS_DB':'0', 'NVIDIA_VISIBLE_DEVICES':'void'}
    compose = {'services':{'cube-proxy-fleet':{'image':IMAGE, 'pull_policy':'never',
        'container_name':'cube-proxy-fleet', 'network_mode':'host', 'runtime':'runc',
        'restart':'no', 'cpus':2, 'mem_limit':'1g', 'pids_limit':128,
        'security_opt':['no-new-privileges:true'], 'environment':env,
        'volumes':['/data/log/cube-proxy-fleet:/data/log/cube-proxy',
                   str(certs)+':/usr/local/openresty/nginx/certs:ro',
                   str(DEST/'global.conf')+':/usr/local/openresty/nginx/conf/global/global.conf:ro',
                   str(DEST/'nginx.conf')+':/usr/local/openresty/nginx/conf/nginx.conf:ro']}}}
    Path('/data/log/cube-proxy-fleet').mkdir(parents=True, exist_ok=True)
    (DEST/'compose.json').write_text(json.dumps(compose))
    print('Private B200 proxy configuration prepared; not started.')


if __name__ == '__main__':
    main()
