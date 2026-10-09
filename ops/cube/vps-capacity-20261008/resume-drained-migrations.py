"""Continue an intentionally drained, successful batch at reviewed concurrency."""
import contextlib,importlib.util,json,os,pathlib,sqlite3,sys
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
prior_name,name=sys.argv[1:];assert prior_name.isalnum() and name.isalnum() and prior_name!=name
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
    prior=root/('parallel-reprofile-'+prior_name);paused=b.strict(b.trusted(prior/'paused.json'))
    assert not paused['complete'] and paused['results'] and all(r['passed'] for r in paused['results'])
    barrier=root/'restore-barrier.json';old=b.strict(b.trusted(barrier));assert old['owner']==prior.name
    with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
        assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
        assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
        assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
    b.atomic(prior/'superseded.json',b.encoded({'by':'parallel-reprofile-'+name,'operator_drained':True,'completed_results_passed':True,'next_transfer_concurrency':4,'provider_mutations_serialized':True}))
    old['owner']='parallel-transition';b.atomic(barrier,b.encoded(old))
os.execv('/usr/bin/python3',['/usr/bin/python3',str(root/'parallel-migrations.py'),'reprofile',name,'4'])
