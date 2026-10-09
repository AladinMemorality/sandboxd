"""Complete the two reviewed exceptions and verify the full reprofile batch."""
import importlib.util,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
out=root/'parallel-reprofile-768f';assert out.is_dir() and not (out/'complete.json').exists()
derja='01M46NANDW8YVG3Z2F2EXZCY1F';new='01M4EP7FC5TTW0RZS8SKTGX9RV'
deadline=time.monotonic()+900
while not (root/'recovery-moves'/('vps-reprofile-'+derja.lower()+'-768-01')/'complete.json').exists():
 assert time.monotonic()<deadline,'Static app verification remains incomplete';time.sleep(5)
while not (root/'recovery-moves'/('vps-reprofile-'+new.lower()+'-768-02')/'complete.json').exists():
 assert time.monotonic()<deadline,'Redirect app verification remains incomplete';time.sleep(5)
with b.locked():
 prior=root/'parallel-reprofile-768e';paused=b.strict(b.trusted(prior/'paused.json'))
 assert not paused['remaining'] and len(paused['results'])==10
 assert {r['sandbox_id'] for r in paused['results'] if not r['passed']}=={derja,new}
 results=[]
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
  for row in paused['results']:
   sid=row['sandbox_id'];attempt='02' if sid==new else '01'
   journal=root/'recovery-moves'/('vps-reprofile-'+sid.lower()+'-768-'+attempt)
   receipt=json.loads((journal/'complete.json').read_text());assert receipt['restored'] and receipt['all_content_verified']
   target=json.loads((journal/'worker-job.PRIVATE.json').read_text())['runtime_id']
   assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()==(target,)
   results.append({'sandbox_id':sid,'passed':True,'journal':journal.name})
 barrier=root/'restore-barrier.json';assert json.loads(barrier.read_text())['owner']==prior.name
 b.atomic(prior/'superseded.json',b.encoded({'by':out.name,'all_ten_verified':True}))
 barrier.unlink()
 b.atomic(out/'complete.json',b.encoded({'complete':True,'results':results,'model_calls':False,'b200_contacted':False}))
 print(json.dumps({'reprofiles_verified':10}),flush=True)
