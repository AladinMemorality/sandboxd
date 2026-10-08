"""Validate local converted archives against the live runtime import contract."""
import json,pathlib,subprocess,time,os
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
results=[]
for receipt in sorted((root/'recovery-prepared').glob('*/export-result.PRIVATE.json')):
 p=subprocess.run([str(root/'artifact-validator'),str(receipt.parent)],capture_output=True,text=True,timeout=180)
 try:result=json.loads(p.stdout)
 except ValueError:result={'valid':False,'error':'validator did not return a receipt','exit_code':p.returncode}
 result['sandbox_id']=receipt.parent.name;results.append(result)
 tmp=root/'artifact-validation.PRIVATE.json.tmp';tmp.write_text(json.dumps({'at':time.time(),'results':results}));os.replace(tmp,root/'artifact-validation.PRIVATE.json')
print(json.dumps({'checked':len(results),'passed':sum(r['valid'] for r in results),'workers_contacted':[]}))
