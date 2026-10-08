"""Preserve the reviewed app's login/session data without exposing its values."""
import hashlib,json,os,pathlib,shutil,sqlite3,subprocess,zipfile
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M3YSBA5CHS0XSFWAZ6RFQ477'
source=root/'recovery-prepared'/sid;receipt=json.loads((source/'export-result.PRIVATE.json').read_text())
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
    assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()[0]==receipt['runtime_id']
    assert [r[0] for r in db.execute('select task_id from task where sandbox_id=? order by task_id',(sid,))]==receipt['task_ids']
name='.local/share/aladin-portfolio/auth.json'
with zipfile.ZipFile(source/'home.zip') as z:
    data=z.read(name);value=json.loads(data)
    assert set(value)=={'hash','salt','secret'} and all(isinstance(x,str) and x for x in value.values())
    assert len(data)==318
receipt['home_manifest']['owner_data_files']=[{'path':name,'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}]
out=root/'recovery-prepared-v2'/sid;out.mkdir(mode=0o700,exist_ok=False)
for role in ['workspace','home','history']:
    shutil.copyfile(source/(role+'.zip'),out/(role+'.zip'));receipt['artifacts'][role]['local_path']=str(out/(role+'.zip'))
(out/'export-result.PRIVATE.json').write_text(json.dumps(receipt))
subprocess.run([str(root/'owner-data-release-87a99c7/recovery-artifact-validator'),str(out)],check=True)
print(json.dumps({'prepared':True,'sandbox_id':sid,'application_auth_preserved':True,'credential_values_disclosed':False}))
