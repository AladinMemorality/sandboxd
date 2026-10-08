"""Bounded synthetic cloud tool-call concurrency; no customer data or B200."""
import concurrent.futures,json,pathlib,subprocess,threading,time,urllib.request,urllib.error
ROOT=pathlib.Path('/opt/baarcha/operations/vps-build-concurrency-20261008-01')
pid=subprocess.check_output(['systemctl','show','baarcha-landing','-p','MainPID','--value']).decode().strip()
env=dict(x.split('=',1) for x in pathlib.Path('/proc/'+pid+'/environ').read_bytes().decode().split('\0') if '=' in x)
key=env.get('OPENROUTER_API_KEY','').strip();assert key
body={'model':'z-ai/glm-5.3-flash','max_tokens':128,'stream':False,'messages':[{'role':'user','content':'Call health_probe with ok true. This is a synthetic infrastructure concurrency check.'}],'tools':[{'name':'health_probe','description':'Record the synthetic check','input_schema':{'type':'object','properties':{'ok':{'type':'boolean'}},'required':['ok'],'additionalProperties':False}}],'tool_choice':{'type':'tool','name':'health_probe'}}
deadline=time.monotonic()+900
while True:
 assert time.monotonic()<deadline,'Benchmark did not reach build phase'
 if (ROOT/'STOP').exists():raise RuntimeError('Host monitor stopped test')
 try:
  p=json.loads((ROOT/'progress.json').read_text())
  if p['phase']=='build-start' and p['detail']==50:break
  if p['phase']=='finished':raise RuntimeError('Benchmark finished before trigger')
 except FileNotFoundError:pass
 time.sleep(.5)
barrier=threading.Barrier(50)
def run(i):
 barrier.wait(timeout=10);began=time.monotonic();receipt={'i':i,'ok':False,'started':time.time()}
 req=urllib.request.Request('https://openrouter.ai/api/v1/messages',data=json.dumps(body).encode(),headers={'content-type':'application/json','Authorization':'Bearer '+key,'anthropic-version':'2023-06-01'},method='POST')
 try:
  with urllib.request.urlopen(req,timeout=60) as r:result=json.load(r);receipt['http']=r.status
  calls=[x for x in result.get('content',[]) if x.get('type')=='tool_use']
  receipt['ok']=len(calls)==1 and calls[0]['name']=='health_probe' and calls[0]['input']=={'ok':True}
  receipt['usage']=result.get('usage',{})
 except urllib.error.HTTPError as e:receipt['http']=e.code
 except Exception as e:receipt['error_type']=type(e).__name__
 receipt['seconds']=time.monotonic()-began;return receipt
began=time.monotonic()
with concurrent.futures.ThreadPoolExecutor(max_workers=50) as pool:results=list(pool.map(run,range(50)))
report={'concurrent':50,'b200_contacted':False,'provider':'OpenRouter','model':body['model'],'kind':'synthetic tool calls, not full coding-agent sessions','passed':sum(x['ok'] for x in results),'seconds':time.monotonic()-began,'results':results}
(ROOT/'cloud-concurrency.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k!='results'}))
