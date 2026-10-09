"""Wait for the reviewed placement/density/backup proofs, then finish rollout."""
import json,os,pathlib,time
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');stage=root/'finish-vps-recovery-13'
deadline=time.monotonic()+18000
while not (stage/'complete.json').exists():
 assert not (stage/'failed.json').exists(),'Recovery stage failed; no supervisor rollout started'
 assert time.monotonic()<deadline,'Recovery deadline exceeded'
 time.sleep(10)
assert json.loads((stage/'complete.json').read_text())['complete']
os.execv('/usr/bin/python3',['/usr/bin/python3',str(root/'upgrade-remaining-supervisors.py')])
