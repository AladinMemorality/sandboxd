"""Follow local Vite module imports over HTTP without executing application JS."""
import http.client,json,pathlib,subprocess,time
from html.parser import HTMLParser
from urllib.parse import urljoin,urlsplit

_ALLOWED=('/assets/','/src/','/node_modules/','/@vite/','/@id/','/@fs/','/@react-refresh')
class Entries(HTMLParser):
    def __init__(self):super().__init__();self.paths=[]
    def handle_starttag(self,tag,attrs):
        attrs=dict(attrs)
        if tag=='script' and attrs.get('src'):self.paths.append(attrs['src'])
        if tag=='link' and attrs.get('rel')=='stylesheet' and attrs.get('href'):self.paths.append(attrs['href'])
def local_module(base,spec,static_root=None):
    if not spec.startswith(('/','./','../')) or spec.startswith('//'):return None
    resolved=urlsplit(urljoin('http://preview.invalid'+base,spec))
    extra=static_root and static_root!='/' and resolved.path.startswith(static_root) and pathlib.PurePosixPath(resolved.path).suffix in ('.js','.mjs','.css','.json','.svg','.png','.jpg','.webp','.wasm','.woff','.woff2')
    if resolved.netloc!='preview.invalid' or not (resolved.path.startswith(_ALLOWED) or extra):return None
    return resolved.path+('?' + resolved.query if resolved.query else '')
def entries(html,base="/",static_root=None):
    parser=Entries();parser.feed(html.decode('utf-8',errors='replace'))
    paths=[raw if raw.startswith(('/', './', '../')) or ':' in raw else './'+raw for raw in parser.paths]
    return [p for raw in paths if (p:=local_module(base,raw,static_root))]
def imports(path,source,static_root=None):
    # CSS and binary assets are fetched but are not JavaScript modules.
    if pathlib.PurePosixPath(urlsplit(path).path).suffix in ('.css','.svg','.png','.jpg','.webp','.wasm','.woff','.woff2'):return []
    parser=pathlib.Path(__file__).with_name('module-parser')/'imports.cjs'
    result=subprocess.run(['node',str(parser)],input=source,capture_output=True,timeout=15)
    assert result.returncode==0,'JavaScript module parse failed'
    specs=json.loads(result.stdout)
    return list(dict.fromkeys(p for spec in specs if (p:=local_module(path,spec,static_root))))

def guest_memory(runtime_id):
    # Root-only read of counters inside this VPS guest, with no application JS.
    assert len(runtime_id)==32 and all(c in '0123456789abcdef' for c in runtime_id)
    guest="import pathlib,json;p=pathlib.Path;m={l.split(':',1)[0]:int(l.split()[1])*1024 for l in p('/proc/meminfo').read_text().splitlines()};v=dict(l.split() for l in p('/proc/vmstat').read_text().splitlines());print('RUNTIME_RECEIPT='+json.dumps({'oom_kill':int(v['oom_kill']),'available_bytes':m['MemAvailable'],'total_bytes':m['MemTotal']}))"
    inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-process-recovery-a583d45');import worker; print(json.dumps(worker.execute("+repr(runtime_id)+","+repr(guest)+",'probe',b'')))"
    ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
    for attempt in range(3):
        result=subprocess.run(ssh+['python3 -'],input=inner.encode(),capture_output=True,timeout=45)
        if result.returncode==0:
            try:return json.loads(result.stdout)
            except ValueError:pass
        if attempt<2:time.sleep(attempt+1)
    raise RuntimeError('Read-only guest memory probe unavailable after three attempts')


def redirect_path(base,location):
    resolved=urlsplit(urljoin('http://preview.invalid'+base,location))
    assert resolved.netloc=='preview.invalid' and resolved.scheme=='http'
    assert resolved.path.endswith('.html') and not resolved.query and not resolved.fragment
    assert not any(c in resolved.path for c in ('\r','\n','\\'))
    return resolved.path

def page(origin,headers,timeout=15):
    """Read HTML, following at most three same-origin HTML-only redirects."""
    path='/'
    for attempt in range(4):
        connection=http.client.HTTPConnection(*origin,timeout=timeout)
        try:
            connection.request('GET',path,headers=headers);response=connection.getresponse()
            if response.status in (301,302,303,307,308):
                location=response.getheader('Location');assert location and attempt<3
                path=redirect_path(path,location);continue
            if response.status!=200:raise RuntimeError('Preview HTML returned '+str(response.status))
            data=response.read(2*1024**2+1);assert len(data)<=2*1024**2
            return path,data
        finally:connection.close()
    raise RuntimeError('Preview redirect limit')
