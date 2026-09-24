import json,pathlib,shlex,subprocess
stage=pathlib.Path('/opt/baarcha-bench/cube-global-20260924/postgres-v4')
ssh=['ssh','-i','/opt/baarcha-bench/cube-20260917/vm-key','-p','19222','-o','BatchMode=yes','root@127.0.0.1']
def remote(args):return subprocess.check_output(ssh+[shlex.join(args)],text=True)
free=json.loads(remote(['python3','-c','import os,json;print(json.dumps({p:os.statvfs(p).f_bavail*os.statvfs(p).f_frsize for p in ["/","/data"]}))']))
if min(free.values())<12*1024**3:raise RuntimeError('fixture disk headroom below12GiB')
tags=['baarcha-cube-reviewed-'+p+':20260924-v4-final' for p in ['marketplace','react-vite','nextjs','node-express','fastapi','worker']]
present = subprocess.run(ssh+[shlex.join(['docker','image','inspect',*tags])],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode == 0
if not present:
 with (stage/'remaining-image-import.log').open('wb') as log:
  save=subprocess.Popen(['docker','save',*tags],stdout=subprocess.PIPE,stderr=log)
  loaded=subprocess.run(ssh+['docker load'],stdin=save.stdout,stdout=log,stderr=log)
  save.stdout.close()
  if save.wait()!=0 or loaded.returncode!=0:raise RuntimeError('candidate image import failed')
results=json.loads((stage/'remaining-templates.json').read_text()) if (stage/'remaining-templates.json').exists() else []
for preset,tag in zip(['marketplace','react-vite','nextjs','node-express','fastapi','worker'],tags):
 if any(r['preset']==preset for r in results):continue
 nested='127.0.0.1:5000/reviewed-'+preset+':20260924-v4'
 remote(['docker','tag',tag,nested]);(stage/(preset+'-push.log')).write_text(remote(['docker','push',nested]))
 created=json.loads(remote(['cubemastercli','tpl','create-from-image','--image',nested,'--alias','baarcha-'+preset+'-reviewed-20260924-v4','--expose-port','3000','--expose-port','3001','--expose-port','3031','--probe','49983','--probe-path','/health','--cpu','1000','--memory','1024','--writable-layer-size','10Gi','--with-cube-ca=false','--deny-out-cidr','0.0.0.0/0','--json','--detach']))
 job=created['job'];(stage/(preset+'-template-job.json')).write_text(json.dumps(created,indent=2))
 with (stage/(preset+'-template-watch.log')).open('wb') as log:subprocess.run(ssh+[shlex.join(['cubemastercli','tpl','watch','--job-id',job['job_id'],'--json'])],stdout=log,stderr=log,check=True)
 status=json.loads(remote(['cubemastercli','tpl','status','--job-id',job['job_id'],'--json']))
 if status['job']['status']!='READY':raise RuntimeError('template not ready')
 digest=subprocess.check_output(['docker','image','inspect',tag,'--format','{{.Id}}'],text=True).strip()
 result={'preset':preset,'template':job['template_id'],'image':tag,'image_digest':digest,'free_bytes_before':free}
 results.append(result);(stage/'remaining-templates.json').write_text(json.dumps(results,indent=2));print(json.dumps(result),flush=True)
