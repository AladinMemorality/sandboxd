"""Retain and reconcile the exact operator SIGINT before restarting its one probe."""
import importlib.util,json,os,pathlib,sqlite3,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sid='01M3EX97H72RTAV9EXB2JMNNBN';rid='f892459082d24fd992c5b72950e7c94a'
out=root/'fleet-wake-validation-01';case=out/(sid+'-'+rid)
with b.locked():
 assert json.loads((root/'fleet-storage-pause-01.json').read_text())
 failure=json.loads((case/'failed.json').read_text());assert failure['type']=='KeyboardInterrupt'
 assert not (case/'passed.json').exists()
 before=json.loads((case/'before.json').read_text())
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row
  actual=dict(db.execute('select s.id,s.status,s.web_port,b.runtime_id,a.worker_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,)).fetchone())
  assert actual==before and actual['runtime_id']==rid and actual['status']=='stopped' and actual['state']=='released' and actual['charged']==0
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
  history=[tuple(r) for r in db.execute('select * from task where sandbox_id=? order by task_id',(sid,))]
 with sqlite3.connect('file:/var/backups/baarcha-vps-source/20261009T162045Z/controller.PRIVATE.sqlite?mode=ro',uri=True) as db:
  assert history==db.execute('select * from task where sandbox_id=? order by task_id',(sid,)).fetchall(),'User task content or status changed'
 dest=out/'interrupted-cases';dest.mkdir(mode=0o700,exist_ok=True);target=dest/case.name;assert not target.exists()
 b.atomic(case/'operator-reconciled.json',b.encoded({'reason':'Intentional storage-pressure SIGINT; cleanup verified, unchanged complete task rows and placement','state':actual,'passed':False,'at':time.time()}))
 case.rename(target);print(json.dumps({'reconciled':True,'original_journal_retained':str(target),'new_probe_required':True}))
