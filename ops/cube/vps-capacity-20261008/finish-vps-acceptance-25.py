"""Resume the reviewed supervisor rejection, then audit; never retry failures."""
import json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
out=root/'finish-vps-acceptance-25';out.mkdir(mode=0o700)
try:
 for stage,args,timeout in [('supervisors',['upgrade-remaining-supervisors.py','--resume-reviewed-motion'],9*3600),('audit',['final-vps-audit.py'],120)]:
  (out/'stage.json').write_text(json.dumps({'stage':stage,'at':time.time()}))
  with (out/(stage+'.PRIVATE.log')).open('wb') as log:
   result=subprocess.run(['/usr/bin/python3',str(root/args[0]),*args[1:]],stdout=log,stderr=subprocess.STDOUT,timeout=timeout)
  assert result.returncode==0,'Stage failed; inspect retained journal: '+stage
 (out/'complete.json').write_text(json.dumps({'complete':True,'model_calls':False,'b200_contacted':False,'at':time.time()}))
except BaseException as error:
 (out/'failed.json').write_text(json.dumps({'type':type(error).__name__,'reason':str(error)[:300],'at':time.time()}));raise
