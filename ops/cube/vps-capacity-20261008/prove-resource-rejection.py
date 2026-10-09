"""Match native pre-allocation rejection to the exact retained create intent."""
import importlib.util,json,pathlib,shlex,subprocess
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');job=root/'recovery-moves/vps-restore-01m3mjeyfz512wya3jvfgk2rqb'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
 intent=b.strict(b.trusted(job/'create-intent.PRIVATE.json'))
 code="""import json,pathlib,sys
wanted=json.load(sys.stdin);requests=[];rejected=[]
for line in pathlib.Path('/data/log/CubeMaster/cubemaster-req.log').open():
 try:v=json.loads(line)
 except ValueError:continue
 if not v.get('@timestamp','').startswith('2026-10-09T08:09:07'):continue
 text=v.get('LogContent','')
 if text.startswith('CreateSandbox:'):
  data=json.loads(text.split(':',1)[1]);labels=data.get('labels',{})
  if labels.get('sandboxd_relocation_id')==wanted['RelocationID'] and labels.get('sandboxd_admission_operation')==wanted['OperationToken']:requests.append(data['requestID'])
 if text.startswith('CreateSandbox_rsp fail:'):
  data=json.loads(text.split(':',1)[1])
  if data['ret']['ret_code']==130597:rejected.append(data['RequestID'])
assert len(requests)==1 and requests[0] in rejected
wanted.update(RequestID=requests[0],HTTPStatus=500,FailureCode=130597)
print(json.dumps(wanted))
"""
 ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
 result=subprocess.run(ssh+['python3 -c '+shlex.quote(code)],input=json.dumps({'RelocationID':job.name,'OperationToken':intent['Admission']['Token']}).encode(),capture_output=True,timeout=30)
 assert result.returncode==0,'No unambiguous pre-allocation rejection proof'
 proof=json.loads(result.stdout);assert not (job/'provider-rejection.json').exists()
 b.atomic(job/'provider-rejection.json',b.encoded(proof));print(json.dumps({'proven_preallocation_rejection':True,'code':proof['FailureCode'],'request_id':proof['RequestID']}))
