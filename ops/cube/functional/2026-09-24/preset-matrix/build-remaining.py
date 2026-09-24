import json,pathlib,shlex,subprocess
stage=pathlib.Path('/opt/baarcha-bench/cube-global-20260924/postgres-v4')
presets=['marketplace','react-vite','nextjs','node-express','fastapi','worker']
for preset in presets:
 tag='baarcha-cube-reviewed-'+preset+':20260924-v4-final'
 with (stage/(preset+'-build.log')).open('wb') as log:
  subprocess.run(['docker','build','--build-arg','REVIEWED_BASE=baarcha-reviewed-base:20260924-v4-final','--build-arg','RUNTIME_PRESET='+preset,'-f',str(stage/'Dockerfile.cube-composed-preset'),'-t',tag,str(stage)],stdout=log,stderr=log,check=True,timeout=600)
 print('BUILT '+preset,flush=True)
