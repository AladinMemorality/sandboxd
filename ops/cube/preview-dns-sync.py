#!/usr/bin/env python3
"""Keep only Baarcha-managed project DNS records aligned with durable placement."""
import json,sqlite3,urllib.request,re,sys
from pathlib import Path
CONFIG=Path('/etc/baarcha-preview/dns.json')
MARK='Baarcha managed project preview'
def sync():
 cfg=json.loads(CONFIG.read_text());zone=cfg['zone']
 def api(path,method='GET',body=None):
  req=urllib.request.Request('https://api.cloudflare.com/client/v4'+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+cfg['token'],'Content-Type':'application/json'},method=method)
  with urllib.request.urlopen(req,timeout=20) as r:x=json.load(r)
  if not x.get('success'):raise RuntimeError('Cloudflare DNS request rejected')
  return x
 records=[];page=1
 while True:
  x=api('/zones/'+zone+'/dns_records?per_page=100&page='+str(page));records+=x['result']
  if page>=x.get('result_info',{}).get('total_pages',1):break
  page+=1
 byname={r['name']:r for r in records}
 db=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)
 rows=db.execute('SELECT s.id,s.web_port,a.worker_id FROM sandbox s JOIN runtime_binding b ON b.sandbox_id=s.id JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE b.provider=?',('cube',)).fetchall();db.close()
 changed=0
 for sid,port,worker in rows:
  if not re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}',sid) or not isinstance(port,int) or not 0<port<65536 or port in (3031,49983) or worker not in cfg['tunnels']:continue
  name='s-'+sid.lower()+'-'+str(port)+'.'+cfg['domain'];target=cfg['tunnels'][worker]+'.cfargotunnel.com';old=byname.get(name)
  if old and old.get('comment')!=MARK:raise RuntimeError('Unmanaged preview DNS record conflict')
  if old and old['type']=='CNAME' and old['content']==target and old['proxied']:continue
  body={'name':name,'type':'CNAME','content':target,'proxied':True,'ttl':1,'comment':MARK}
  api('/zones/'+zone+'/dns_records'+('/'+old['id'] if old else ''),'PUT' if old else 'POST',body);changed+=1
 print(json.dumps({'projects':len(rows),'dns_changes':changed}))
if __name__=='__main__':sync()
