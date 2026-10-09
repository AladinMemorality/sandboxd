"""Follow local Vite module imports over HTTP without executing application JS."""
import json,pathlib,subprocess
from html.parser import HTMLParser
from urllib.parse import urljoin,urlsplit

_ALLOWED=('/src/','/node_modules/','/@vite/','/@id/','/@fs/','/@react-refresh')
class Entries(HTMLParser):
    def __init__(self):super().__init__();self.paths=[]
    def handle_starttag(self,tag,attrs):
        attrs=dict(attrs)
        if tag=='script' and attrs.get('src'):self.paths.append(attrs['src'])
        if tag=='link' and attrs.get('rel')=='stylesheet' and attrs.get('href'):self.paths.append(attrs['href'])
def local_module(base,spec):
    if not spec.startswith(('/','./','../')) or spec.startswith('//'):return None
    resolved=urlsplit(urljoin('http://preview.invalid'+base,spec))
    if resolved.netloc!='preview.invalid' or not resolved.path.startswith(_ALLOWED):return None
    return resolved.path+('?' + resolved.query if resolved.query else '')
def entries(html):
    parser=Entries();parser.feed(html.decode('utf-8',errors='replace'))
    return [p for raw in parser.paths if (p:=local_module('/',raw))]
def imports(path,source):
    # CSS and binary assets are fetched but are not JavaScript modules.
    if pathlib.PurePosixPath(urlsplit(path).path).suffix in ('.css','.svg','.png','.jpg','.webp','.wasm','.woff','.woff2'):return []
    parser=pathlib.Path(__file__).with_name('module-parser')/'imports.cjs'
    result=subprocess.run(['node',str(parser)],input=source,capture_output=True,timeout=15)
    assert result.returncode==0,'JavaScript module parse failed'
    specs=json.loads(result.stdout)
    return list(dict.fromkeys(p for spec in specs if (p:=local_module(path,spec))))
