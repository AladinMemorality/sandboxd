"""Recover the two quota-interrupted exports, then continue untouched projects."""
import contextlib,importlib.util,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
out=root/'resume-after-account-guard-02';out.mkdir(mode=0o700)
probe=root/'recovery-moves/vps-reprofile-01m2jy1nnjzz05fpc6m464q86g-768-01'
deadline=time.monotonic()+600
while not (probe/'complete.json').exists():
    assert time.monotonic()<deadline,'Fenced target recovery has not completed'
    time.sleep(5)
assert json.loads((probe/'complete.json').read_text())['restored']
ids=['01M1XFEHGCQ2HV5NNJBXNQK3WE','01M2EAAQ1M9FZDY0F1BQPRM1CA']
attempts={ids[0]:'03',ids[1]:'02'}
for sid in ids:
    with (out/(sid+'.PRIVATE.log')).open('wb') as log:
        p=subprocess.run(['/usr/bin/python3',str(root/'reprofile-vps.py'),sid,attempts[sid]],stdout=log,stderr=subprocess.STDOUT)
    assert p.returncode==0,'Recovered export needs review'
    print(json.dumps({'restored':sid}),flush=True)
with b.locked():
    prior=root/'parallel-reprofile-768d';paused=b.strict(b.trusted(prior/'paused.json'))
    assert set(r['sandbox_id'] for r in paused['results'] if not r['passed'])==set(ids+['01M2JY1NNJZZ05FPC6M464Q86G'])
    for sid in ids:
        receipts=list((root/'recovery-moves').glob('vps-reprofile-'+sid.lower()+'-*-'+attempts[sid]+'/complete.json'))
        assert len(receipts)==1 and json.loads(receipts[0].read_text())['restored']
    with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
        assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
        assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
        assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
    barrier=root/'restore-barrier.json';value=b.strict(b.trusted(barrier));assert value['owner']==prior.name
    b.atomic(prior/'superseded.json',b.encoded({'by':'parallel-reprofile-768e','interrupted_exports_recovered':True,'readonly_probe_verified':True,'billing_guard_verified':True}))
    value['owner']='parallel-transition';b.atomic(barrier,b.encoded(value))
os.execv('/usr/bin/python3',['/usr/bin/python3',str(root/'parallel-migrations.py'),'reprofile','768e','4'])
