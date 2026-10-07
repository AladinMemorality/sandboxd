import subprocess,json,pathlib,sqlite3,os
os.umask(0o077);r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007')
x=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];e=dict(v.split('=',1) for v in x['Config']['Env']);f=json.loads(e['SANDBOXD_CUBE_FLEET']);w=next(w for w in f['workers'] if w['id']=='vps')
p=json.loads(pathlib.Path('/opt/baarcha/operations/derja-disk-recovery-20261007/operator-config.private.json').read_text());p['Policy']=w['admission'];p['ProxyURL']=w['proxy_url'];(r/'operator-config.private.json').write_text(json.dumps(p));(r/'operator-identity.json').write_text(json.dumps({'controller_id':x['Id']}))
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as source:
 with sqlite3.connect(r/'preflight.db') as dest:source.backup(dest)
print('private VPS operator policy staged')
