"""Close the retained failed-batch journal only after every canonical target is verified."""
import importlib.util,json,os,pathlib,sqlite3,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');batch=root/'parallel-restore-08'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
 assert not (batch/'complete.json').exists()
 paused=json.loads((batch/'paused.json').read_text());assert len(paused['results'])==15 and not paused['remaining']
 assert [x['sandbox_id'] for x in paused['results'] if not x['passed']]==['01M45GE5BT8GTFEA2VV2R9KCEZ']
 assert json.loads((root/'vite-syntax-repair/complete.json').read_text())['restored']
 verified=[]
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute('select a.worker_id,count(*) from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id group by a.worker_id').fetchall()==[('vps',134)]
  assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
  for row in paused['results']:
   sid=row['sandbox_id'];runtime=db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()[0]
   matches=[]
   for job in (root/'recovery-moves').glob('vps-restore-'+sid.lower()+'*'):
    if not (job/'complete.json').exists():continue
    value=json.loads((job/'complete.json').read_text())
    if value['restored'] and json.loads((job/'worker-job.PRIVATE.json').read_text())['runtime_id']==runtime:matches.append(job.name)
   assert len(matches)==1;verified.append({'sandbox_id':sid,'journal':matches[0]})
 barrier=root/'restore-barrier.json';value=json.loads(barrier.read_text());assert value['owner']==batch.name
 result={'complete':True,'count':15,'reconciled_after_review':True,'failed_journal_retained':True,'verified':verified,'at':time.time()}
 b.atomic(batch/'complete.json',b.encoded(result));barrier.unlink();print(json.dumps(result),flush=True)
