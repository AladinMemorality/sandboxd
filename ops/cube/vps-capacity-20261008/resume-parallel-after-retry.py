"""Supersede the paused pre-mutation rejection batch after its explicit recovery."""
import contextlib,importlib.util,json,os,pathlib,sqlite3
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
    assert json.loads((root/'concurrent-resume-7745f34/result.json').read_text())['passed']
    completed=[p for p in (root/'recovery-moves').glob('vps-reprofile-01m1hh5dt8fvcp5tnresedjbh6-*-02/complete.json')]
    assert len(completed)==1 and json.loads(completed[0].read_text())['restored']
    prior=root/'parallel-reprofile-768b';assert not json.loads((prior/'paused.json').read_text())['complete']
    barrier=root/'restore-barrier.json';old=b.strict(b.trusted(barrier));assert old['owner']==prior.name
    with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
        assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
        assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
        assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
        assert db.execute("select template_id from runtime_binding where sandbox_id='01M1HH5DT8FVCP5TNRESEDJBH6'").fetchone()[0]!='tpl-78e4edb3d629465e9d8372c1'
    b.atomic(prior/'superseded.json',b.encoded({'by':'parallel-reprofile-768c','rejected_start_reconciled':True,'replacement_verified':True}))
    old['owner']='parallel-transition';b.atomic(barrier,b.encoded(old))
os.execv('/usr/bin/python3',['/usr/bin/python3',str(root/'parallel-migrations.py'),'reprofile','768c'])
