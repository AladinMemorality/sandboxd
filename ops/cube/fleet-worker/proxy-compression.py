#!/usr/bin/env python3
"""Stage/apply modern JavaScript gzip types on the pinned B200 proxy.

Apply only while the coordinator holds all shared operator locks. Staging and
measurement do not change live routing. Native archive transfers remain local.
"""
import gzip,hashlib,http.client,json,os,re,socket,subprocess,sys,time
from pathlib import Path
BASE=Path('/usr/local/services/cubetoolbox/cubeproxy-fleet')
CONF=BASE/'nginx.conf';ROOT=BASE/'compression-20260927'
IMAGE='cube-sandbox-int.tencentcloudcr.com/cube-sandbox/cube-proxy@sha256:b5a196be3698d820c1ae70a3a7a9fd0439badd43e74a2c4954255f7dc69f86dc'
NGINX='/usr/local/openresty/nginx/sbin/nginx'
def sha(data):return hashlib.sha256(data).hexdigest()
def run(*args):return subprocess.run(args,check=True,capture_output=True,timeout=30)
def identity():
    v=json.loads(run('docker','inspect','cube-proxy-fleet').stdout)[0]
    assert v['Config']['Image']==IMAGE and v['State']['Running']
    mounts=[m for m in v['Mounts'] if m['Destination']=='/usr/local/openresty/nginx/conf/nginx.conf']
    assert len(mounts)==1 and mounts[0]['Source']==str(CONF) and not mounts[0]['RW']
    return v['Id']
def stage():
    identity();ROOT.mkdir(mode=0o700,exist_ok=False)
    before=CONF.read_bytes();text=before.decode()
    matches=list(re.finditer(r'(?m)^\s*gzip_types\s+([^;]+);',text));assert len(matches)==1
    found=matches[0];types=found.group(1).split()
    for value in ['application/javascript','text/javascript']:
        if value not in types:types.append(value)
    after=(text[:found.start(1)]+' '.join(types)+text[found.end(1):]).encode();assert after!=before
    (ROOT/'before.conf').write_bytes(before);(ROOT/'candidate.conf').write_bytes(after)
    candidate='/usr/local/openresty/nginx/conf/nginx.compression-candidate.conf'
    run('docker','cp',str(ROOT/'candidate.conf'),'cube-proxy-fleet:'+candidate)
    tested=run('docker','exec','cube-proxy-fleet',NGINX,'-t','-c',candidate)
    (ROOT/'syntax-check.log').write_bytes(tested.stderr)
    report=dict(before_sha256=sha(before),after_sha256=sha(after),syntax_valid=True)
    (ROOT/'staged.json').write_text(json.dumps(report));print(json.dumps(report))
def write_live(data):
    # Preserve the inode of the file already bind-mounted read-only in nginx.
    with CONF.open('r+b') as f:f.write(data);f.truncate();f.flush();os.fsync(f.fileno())
def apply():
    ident=identity();report=json.loads((ROOT/'staged.json').read_text())
    before=(ROOT/'before.conf').read_bytes();after=(ROOT/'candidate.conf').read_bytes()
    assert sha(CONF.read_bytes())==sha(before)==report['before_sha256'] and sha(after)==report['after_sha256']
    try:
        write_live(after)
        run('docker','exec','cube-proxy-fleet',NGINX,'-t')
        run('docker','exec','cube-proxy-fleet',NGINX,'-s','reload')
        assert identity()==ident
        report.update(applied=True,container_id=ident)
        (ROOT/'applied.json').write_text(json.dumps(report));print(json.dumps(report))
    except BaseException:
        write_live(before);run('docker','exec','cube-proxy-fleet',NGINX,'-t');run('docker','exec','cube-proxy-fleet',NGINX,'-s','reload');raise
def measure():
    job=json.loads(sys.stdin.buffer.read(16385));assert len(json.dumps(job))<=16384
    assert re.fullmatch('[a-f0-9]{32}',job['runtime_id'])
    headers=job['headers'];assert set(headers)=={'Host','cube-traffic-access-token'}
    assert headers['Host'].startswith('3000-'+job['runtime_id']+'.')
    assert not any('\r' in v or '\n' in v for v in headers.values())
    route=json.loads(run('ip','-j','route','get','10.254.240.2').stdout)[0];assert route['type']=='local' and route['dev']=='lo'
    out=[]
    for encoding in ['identity','gzip']:
        c=http.client.HTTPConnection('10.254.240.2',28080,timeout=30)
        try:
            began=time.monotonic();c.request('GET','/node_modules/.vite/deps/lucide-react.js',headers={**headers,'Accept-Encoding':encoding})
            r=c.getresponse();body=r.read(16*1024**2+1);assert r.status==200 and len(body)<=16*1024**2
            wire=r.getheader('Content-Encoding');decoded=gzip.decompress(body) if wire=='gzip' else body
            assert len(decoded)<=32*1024**2
            out.append(dict(requested_encoding=encoding,response_encoding=wire,wire_bytes=len(body),decoded_bytes=len(decoded),sha256=sha(decoded),content_type=r.getheader('Content-Type'),seconds=time.monotonic()-began))
        finally:c.close()
    assert out[0]['sha256']==out[1]['sha256']
    print(json.dumps(dict(local_origin=True,responses=out)))
if __name__=='__main__':
    assert os.geteuid()==0 and socket.gethostname()=='baarcha-cube-worker-b200-01';os.umask(0o077)
    {'stage':stage,'apply':apply,'measure':measure}[sys.argv[1]]()
