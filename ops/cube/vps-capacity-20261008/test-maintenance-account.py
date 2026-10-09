"""Verify real platform locks and unchanged usage for three stopped test apps."""
import importlib.util,json,pathlib,subprocess
from maintenance_account import account_maintenance
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ids=['01M1XFEHGCQ2HV5NNJBXNQK3WE','01M2EAAQ1M9FZDY0F1BQPRM1CA','01M2JY1NNJZZ05FPC6M464Q86G']
out=root/'maintenance-account-check';out.mkdir(mode=0o700)
def probe(state):
    args=['/opt/baarcha/node22/bin/node','--env-file=/opt/baarcha/landing.env',str(root/'check-maintenance-account.mjs'),state]
    p=subprocess.run(args,capture_output=True,timeout=30);assert p.returncode==0,'Account lock probe failed'
    return json.loads(p.stdout)
with b.locked():
    with account_maintenance(ids,out):held=probe('held')
    after=probe('released')
    assert [(r['owner'],r['usage']) for r in held]==[(r['owner'],r['usage']) for r in after]
    receipt={'passed':True,'protected_accounts':3,'same_platform_lock_keys':True,'locks_released':True,'daily_usage_unchanged_during_maintenance':True}
    b.atomic(out/'result.json',b.encoded(receipt));print(json.dumps(receipt))
