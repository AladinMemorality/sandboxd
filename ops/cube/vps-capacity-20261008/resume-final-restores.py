"""Resolve the exact disk-quota rejection, then restore the twenty untouched apps."""
import importlib.util,json,os,pathlib,sqlite3,subprocess
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'resume-final-restores';out.mkdir(mode=0o700)
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ids=['01M3MJEYFZ512WYA3JVFGK2RQB','01M3MJR6YHX4YRY4GSDJPQ771X']
for sid in ids:
 with (out/(sid+'.PRIVATE.log')).open('wb') as log:
  p=subprocess.run(['/usr/bin/python3',str(root/'resume-resource-rejected-restore.py'),sid,'balanced'],stdout=log,stderr=subprocess.STDOUT,timeout=1800)
 assert p.returncode==0,'Resource-rejection recovery requires review'
 print(json.dumps({'restored':sid}),flush=True)
with b.locked():
 prior=root/'parallel-restore-06';paused=b.strict(b.trusted(prior/'paused.json'))
 assert len(paused['results'])==39 and len(paused['remaining'])==20
 assert {r['sandbox_id'] for r in paused['results'] if not r['passed']}==set(ids)
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
  for result in paused['results']:
   sid=result['sandbox_id'];job=root/'recovery-moves'/('vps-restore-'+sid.lower())
   assert json.loads((job/'complete.json').read_text())['restored']
   runtime=json.loads((job/'worker-job.PRIVATE.json').read_text())['runtime_id']
   assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()==(runtime,)
  for row in paused['remaining']:
   assert db.execute('select a.worker_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id where b.sandbox_id=?',(row['sandbox_id'],)).fetchone()==('b200-01',)
   assert all(not (root/'recovery-moves'/j).exists() for j in row['journals'])
 prepared=root/'vite-batch-07';prepared.mkdir(mode=0o700)
 b.atomic(prepared/'scope.json',b.encoded({'selected':paused['remaining'],'source_batch':'06','source_contacted':False}))
 b.atomic(prepared/'prepared.json',b.encoded({'prepared':True,'count':20,'existing_verified_local_artifacts':True,'source_batch':'06'}))
 barrier=root/'restore-barrier.json';value=b.strict(b.trusted(barrier));assert value['owner']==prior.name
 b.atomic(prior/'superseded.json',b.encoded({'by':'parallel-restore-07','all_attempted_verified':True,'disk_growth_verified':True}))
 value['owner']='parallel-transition';b.atomic(barrier,b.encoded(value))
os.execv('/usr/bin/python3',['/usr/bin/python3',str(root/'parallel-migrations.py'),'restore','07','4'])
