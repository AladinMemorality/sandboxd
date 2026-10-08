"""Synthetic tool-call verification of the VPS's configured cloud fallback."""
import json,pathlib,subprocess,time,urllib.request,urllib.error
pid=subprocess.check_output(['systemctl','show','baarcha-landing','-p','MainPID','--value']).decode().strip()
env=dict(x.split('=',1) for x in pathlib.Path('/proc/'+pid+'/environ').read_bytes().decode().split('\0') if '=' in x)
key=env.get('OPENROUTER_API_KEY','').strip();assert key,'Cloud fallback key is not configured'
body={'model':'z-ai/glm-5.3-flash','max_tokens':128,'stream':False,'messages':[{'role':'user','content':'Call health_probe with ok true. This is a synthetic infrastructure check.'}],'tools':[{'name':'health_probe','description':'Record the synthetic check','input_schema':{'type':'object','properties':{'ok':{'type':'boolean'}},'required':['ok'],'additionalProperties':False}}],'tool_choice':{'type':'tool','name':'health_probe'}}
req=urllib.request.Request('https://openrouter.ai/api/v1/messages',data=json.dumps(body).encode(),headers={'content-type':'application/json','Authorization':'Bearer '+key,'anthropic-version':'2023-06-01'},method='POST')
began=time.monotonic()
try:
 with urllib.request.urlopen(req,timeout=45) as response:status=response.status;result=json.load(response)
 calls=[x for x in result.get('content',[]) if x.get('type')=='tool_use']
 assert status==200 and len(calls)==1 and calls[0]['name']=='health_probe' and calls[0]['input']=={'ok':True},'Cloud tool-call check failed'
 receipt={'verified':True,'provider':'OpenRouter','model':result.get('model'),'native_anthropic_tools':True,'seconds':time.monotonic()-began,'b200_contacted':False,'at':time.time()}
except urllib.error.HTTPError as error:receipt={'verified':False,'http':error.code,'b200_contacted':False,'at':time.time()}
path=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008/cloud-agent-check.json');path.write_text(json.dumps(receipt));print(json.dumps(receipt));assert receipt['verified']
