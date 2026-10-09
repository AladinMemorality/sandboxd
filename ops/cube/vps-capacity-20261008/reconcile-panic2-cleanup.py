"""Resolve only density04's failed cleanup after verified source replacement."""
import contextlib,importlib.util,json,pathlib,sqlite3,time
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sid='01M2AR44CA9AW2FEFA44KVEAGQ';old='a467801fc987450eab74e99cfe88913d';new='e3a26535a2874c77a799e45f40158cd1'
with b.locked():
 job=root/'recovery-moves/vps-restore-01m2ar44ca9aw2fefa44kveagq-panic2a'
 complete=json.loads((job/'complete.json').read_text());assert complete['restored'] and complete['source_retained'] and complete['all_content_verified']
 for cycle in (1,2):
  r=json.loads((job/f'repeat-wake-{cycle}.json').read_text());assert r['passed'] and r['runtime_id']==new
 assert len(list(job.glob('module-health-*.json')))>=4
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
  assert db.execute('select s.status,r.runtime_id,a.state,a.charged from sandbox s join runtime_binding r on r.sandbox_id=s.id join cube_admission a on a.runtime_id=r.runtime_id where s.id=?',(sid,)).fetchall()==[('stopped',new,'released',0)]
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
  assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
 barrier=root/'restore-barrier.json';assert json.loads(barrier.read_text())=={'purpose':'reviewed-restore-barrier','allowed_sandboxes':[sid],'owner':'real-preview-density-50-balanced-04'}
 failed=root/'real-preview-density-50-balanced-04';cleanup=json.loads((failed/'cleanup.json').read_text());assert cleanup['bindings_preserved'] and cleanup['existing_running_preserved']
 assert [r['sandbox_id'] for r in cleanup['runtimes'] if not r.get('stopped') and not r.get('preserved_for_user_work')]==[sid]
 receipt={'passed':True,'sandbox_id':sid,'old_runtime_id':old,'runtime_id':new,'source_retained':True,'cycles':3,'all_content_verified':True,'previous_cleanup_resolved':True,'model_calls':False,'b200_contacted':False,'at':time.time()}
 assert not (failed/'cleanup-reconciliation.json').exists()
 b.atomic(failed/'cleanup-reconciliation.json',b.encoded(receipt));barrier.unlink();print(json.dumps(receipt))
