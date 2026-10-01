import urllib.request,urllib.parse,json,time,statistics,argparse
parser=argparse.ArgumentParser(description="Compare identical read-only CubeMaster observations on shared and dedicated worker connections.")
parser.add_argument("--master-url",required=True)
parser.add_argument("--runtime-id",required=True)
args=parser.parse_args()
u=args.master_url.rstrip('/')+'/cube/sandbox/info?sandbox_id='+urllib.parse.quote(args.runtime_id,safe='')+'&instance_type=cubebox'
results={'shared':[],'dedicated':[]}
for i in range(12):
 bodies=[]
 for label,headers in [('shared',{}),('dedicated',{'X-Caller':'baarcha-controller'})]:
  t=time.monotonic()
  with urllib.request.urlopen(urllib.request.Request(u,headers=headers),timeout=15) as f:result=json.load(f)
  assert result['ret']['ret_code']==200
  bodies.append(result['data']);results[label].append(round((time.monotonic()-t)*1000))
 assert bodies[0]==bodies[1], 'Dedicated connection changed returned state'
 print(json.dumps({k:v[-1] for k,v in results.items()}),flush=True)
print(json.dumps({k:{'median':statistics.median(v),'max':max(v),'samples':v} for k,v in results.items()}))
