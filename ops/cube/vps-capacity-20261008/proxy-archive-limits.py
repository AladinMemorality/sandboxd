"""Allow bounded streaming private imports through the VPS native proxy only."""
import hashlib,importlib.util,json,os,pathlib,re,socket,subprocess,sys
P=pathlib.Path;os.umask(0o077)
def render(text):
    assert text.count('client_max_body_size 256M;')==1
    pattern=r'(?m)^        location / \{\n.*?^        \}'
    matches=list(re.finditer(pattern,text,re.S));assert len(matches)==3
    assert sum('proxy_pass http://backend;' in m.group() for m in matches)==2
    def replace(m):
        block=m.group()
        if 'proxy_pass http://backend;' not in block:return block
        scoped=block.replace('location / {',r'location ~ ^/import/(?:private-home-v2|private-workspace-v2)$ {',1)
        scoped=scoped.replace('{\n','{\n            client_max_body_size 4295000068; # 4 GiB archive plus framed manifest\n            proxy_request_buffering off;\n',1)
        return scoped+'\n\n'+block
    return re.sub(pattern,replace,text,flags=re.S|re.M)
def worker():
    assert os.geteuid()==0 and socket.gethostname()=='baarcha-cube-worker-01'
    root=P('/root/vps-private-import-limits-20261008');root.mkdir(mode=0o700)
    config=P('/usr/local/services/cubetoolbox/cubeproxy/nginx.conf');nginx='/usr/local/openresty/nginx/sbin/nginx'
    def run(*a):return subprocess.run(a,check=True,capture_output=True,timeout=30)
    before=config.read_bytes();after=render(before.decode()).encode()
    info=json.loads(run('docker','inspect','cube-proxy').stdout)[0];ident=info['Id']
    assert info['State']['Running'] and any(m['Source']==str(config) and m['Destination']=='/usr/local/openresty/nginx/conf/nginx.conf' for m in info['Mounts'])
    (root/'before.conf').write_bytes(before);(root/'candidate.conf').write_bytes(after)
    candidate='/usr/local/openresty/nginx/conf/nginx.private-import-candidate.conf'
    run('docker','cp',str(root/'candidate.conf'),'cube-proxy:'+candidate)
    (root/'syntax-check.log').write_bytes(run('docker','exec','cube-proxy',nginx,'-t','-c',candidate).stderr)
    def write(data):
        with config.open('r+b') as f:f.write(data);f.truncate();f.flush();os.fsync(f.fileno())
    try:
        assert config.read_bytes()==before
        write(after);run('docker','exec','cube-proxy',nginx,'-t');run('docker','exec','cube-proxy',nginx,'-s','reload')
        assert json.loads(run('docker','inspect','cube-proxy').stdout)[0]['Id']==ident
    except BaseException:
        write(before);run('docker','exec','cube-proxy',nginx,'-t');run('docker','exec','cube-proxy',nginx,'-s','reload');raise
    result={'applied':True,'worker':'vps','before_sha256':hashlib.sha256(before).hexdigest(),'after_sha256':hashlib.sha256(after).hexdigest(),'archive_body_limit_bytes':4295000068,'application_body_limit_unchanged':True,'auth_routing_preserved':True,'graceful_reload':True}
    (root/'applied.json').write_text(json.dumps(result));print(json.dumps(result))
if __name__=='__main__':
    if sys.argv[1:]==['--worker']:worker()
    else:
        assert not sys.argv[1:] and socket.gethostname()=='Ubuntu-noble-latest-amd64-base.zst'
        spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
        with b.locked():
            args=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1','python3 - --worker']
            result=subprocess.run(args,input=P(__file__).read_bytes(),capture_output=True,timeout=120)
            assert result.returncode==0,result.stderr.decode()[:1000]
            receipt=json.loads(result.stdout);b.atomic(P(__file__).with_suffix('.json'),b.encoded(receipt));print(json.dumps(receipt))
