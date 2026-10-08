"""Follow local Vite module imports over HTTP without executing application JS."""
import re
from html.parser import HTMLParser
from urllib.parse import urljoin,urlsplit

_ALLOWED=('/src/','/node_modules/','/@vite/','/@id/','/@fs/','/@react-refresh')
_IMPORT=re.compile(r'''(?:\b(?:import|export)\s+(?:[^;\n]*?\s+from\s*)?["']([^"']+)["']|\bimport\s*\(\s*["']([^"']+)["']\s*\))''')
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
    return list(dict.fromkeys(p for match in _IMPORT.finditer(source.decode('utf-8',errors='replace')) if (p:=local_module(path,match.group(1) or match.group(2)))))
