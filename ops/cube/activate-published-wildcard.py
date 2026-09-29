#!/usr/bin/env python3
"""Route unassigned app hostnames to the VPS; retain every explicit editor DNS record."""
import contextlib, fcntl, json, os, pathlib, re, sqlite3, urllib.request
P=pathlib.Path; os.umask(0o077)
ROOT=P('/opt/baarcha-bench/preview-transport-20260929/published-gateway-retry-2')
cfg=json.loads(P('/etc/baarcha-preview/dns.json').read_text())
def api(path,method='GET',body=None):
    req=urllib.request.Request('https://api.cloudflare.com/client/v4'+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+cfg['token'],'Content-Type':'application/json'},method=method)
    with urllib.request.urlopen(req,timeout=20) as response:result=json.load(response)
    assert result.get('success'),'DNS operation refused'
    return result
with contextlib.ExitStack() as stack:
    for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
        f=stack.enter_context(open(name,'a+'));fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
    assert json.loads(P('/etc/baarcha-preview/gateway.json').read_text())['PublishedController']=='http://127.0.0.1:9090'
    zone='/zones/'+cfg['zone']+'/dns_records'
    records=api(zone+'?per_page=1000')['result'];byname={r['name']:r for r in records}
    with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
        rows=db.execute('SELECT s.id,s.web_port,a.worker_id FROM sandbox s JOIN runtime_binding b ON b.sandbox_id=s.id JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE b.provider=?',('cube',)).fetchall()
    names=set()
    for sid,port,worker in rows:
        if not re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}',sid) or not isinstance(port,int) or not 0<port<65536 or port in (3031,49983):continue
        name='s-'+sid.lower()+'-'+str(port)+'.'+cfg['domain'];names.add(name)
        assert byname[name]['content']==cfg['tunnels'][worker]+'.cfargotunnel.com','Editor placement not explicitly routed'
    wildcard=byname['*.'+cfg['domain']]
    assert wildcard['type']=='CNAME' and wildcard['proxied']
    assert wildcard['content']==cfg['tunnels']['b200-01']+'.cfargotunnel.com','Wildcard baseline changed'
    assert not (ROOT/'wildcard-before.json').exists(),'Wildcard release already attempted'
    (ROOT/'wildcard-before.json').write_text(json.dumps(wildcard))
    fields=('name','type','content','ttl','proxied','comment','tags','settings')
    body={k:wildcard[k] for k in fields if k in wildcard};body['content']=cfg['tunnels']['vps']+'.cfargotunnel.com'
    api(zone+'/'+wildcard['id'],'PUT',body)
    # Remove only records created by this rollout's aborted per-app DNS pass.
    # They are redundant with the wildcard. Earlier/user-managed records stay.
    created=[r for r in records if r['name'].startswith('p-') and 's-'+r['name'][2:] in names and r.get('comment')=='Baarcha managed project preview' and r['content']==body['content'] and '2026-09-29T13:06:00' <= r.get('created_on','') < '2026-09-29T13:12:00']
    (ROOT/'redundant-created-dns.json').write_text(json.dumps(created))
    for record in created:api(zone+'/'+record['id'],'DELETE')
    print(json.dumps({'wildcard_worker':'vps','editor_records_preserved':len(names),'redundant_records_removed':len(created)}))
