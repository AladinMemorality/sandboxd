"""Run reviewed VPS storage changes and acceptance in order, stopping on failure."""
import json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'storage-density-followthrough-14';out.mkdir(mode=0o700)
def wait_file(path,unit,timeout):
 deadline=time.monotonic()+timeout
 while not path.exists():
  state=subprocess.check_output(['systemctl','show',unit,'-p','ActiveState','--value'],text=True).strip()
  assert state not in ('failed','inactive'),'Prerequisite ended without proof: '+unit
  assert time.monotonic()<deadline,'Prerequisite deadline: '+unit
  time.sleep(10)
def run(stage,script,timeout,*args):
 (out/'stage.json').write_text(json.dumps({'stage':stage,'at':time.time()}));print(stage,flush=True)
 with (out/(stage+'.PRIVATE.log')).open('wb') as log:
  result=subprocess.run(['/usr/bin/python3',str(root/script),*args],stdout=log,stderr=subprocess.STDOUT,timeout=timeout)
 assert result.returncode==0,'Stage failed; retained diagnostics: '+stage
try:
 wait_file(root/'cold-archive-move/complete.json','baarcha-vps-archive-cold-nvme.service',10800)
 assert json.loads((root/'cold-archive-move/complete.json').read_text())['all_data_preserved']
 wait_file(root/'resume-retry-release-d5b07bb/built.json','baarcha-vps-build-d5b07bb.service',1800)
 run('controller','deploy-storage-grant-fix.py',1200,'d5b07bb')
 run('data-growth','grow-data-784.py',600)
 # Give the independent storage observer time to publish the new filesystem size.
 time.sleep(35)
 run('density-and-backup','finish-vps-recovery.py',9*3600)
 run('supervisors','upgrade-remaining-supervisors.py',2*3600)
 run('audit','final-vps-audit.py',120)
 (out/'complete.json').write_text(json.dumps({'complete':True,'b200_contacted':False,'model_calls':False,'at':time.time()}))
except BaseException as error:
 (out/'failed.json').write_text(json.dumps({'type':type(error).__name__,'reason':str(error)[:400],'at':time.time()}))
 raise
