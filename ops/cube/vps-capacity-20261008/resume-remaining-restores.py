"""Continue only the untouched entries after reconciling the updater-lock exit."""
import importlib.util,json,os,pathlib,sqlite3,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sid='01M39MX85MJJ1HNGVAY4W3RQZH';deadline=time.monotonic()+1800
while not (root/'recovery-moves'/('vps-restore-'+sid.lower())/'complete.json').exists():
 assert time.monotonic()<deadline,'Updater-lock target not yet verified';time.sleep(5)
with b.locked():
 prior=root/'parallel-restore-05';paused=b.strict(b.trusted(prior/'paused.json'))
 assert len(paused['results'])==4 and len(paused['remaining'])==59
 assert {r['sandbox_id'] for r in paused['results'] if not r['passed']}=={sid}
 for result in paused['results']:
  job=root/'recovery-moves'/('vps-restore-'+result['sandbox_id'].lower())
  assert json.loads((job/'complete.json').read_text())['restored']
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
  for row in paused['remaining']:
   assert db.execute('select a.worker_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id where b.sandbox_id=?',(row['sandbox_id'],)).fetchone()==('b200-01',)
   assert all(not (root/'recovery-moves'/j).exists() for j in row['journals'])
 prepared=root/'vite-batch-06';prepared.mkdir(mode=0o700)
 b.atomic(prepared/'scope.json',b.encoded({'selected':paused['remaining'],'source_batch':'05','source_contacted':False}))
 b.atomic(prepared/'prepared.json',b.encoded({'prepared':True,'count':59,'existing_verified_local_artifacts':True,'source_batch':'05'}))
 barrier=root/'restore-barrier.json';value=b.strict(b.trusted(barrier));assert value['owner']==prior.name
 b.atomic(prior/'superseded.json',b.encoded({'by':'parallel-restore-06','first_four_verified':True,'updater_wait_fixed':True}))
 value['owner']='parallel-transition';b.atomic(barrier,b.encoded(value))
os.execv('/usr/bin/python3',['/usr/bin/python3',str(root/'parallel-migrations.py'),'restore','06','4'])
