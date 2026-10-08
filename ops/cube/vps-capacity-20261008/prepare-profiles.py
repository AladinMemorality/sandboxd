import json,pathlib,subprocess,time
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
for script in ['prepare-standard-1024.py','prepare-builder-3072.py']:
 subprocess.run(['/usr/bin/python3',str(root/script)],check=True)
(root/'profiles-ready.json').write_text(json.dumps({'complete':True,'at':time.time(),'trim_complete':False}))
