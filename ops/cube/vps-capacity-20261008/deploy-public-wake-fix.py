"""Quiesce the deployer's existing timer list before building the tested release."""
import json,os,pathlib,subprocess,time
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
out=root/'public-wake-deploy-02';out.mkdir(mode=0o700)
sha='69936b503843b597c9f0a938e6c78e25a5fa51b6'
# Exactly the existing deployer list; no service is invoked or terminated here.
names='baarcha-runtime-recovery baarcha-runtime-repair-agent baarcha-runtime-watchdog baarcha-sandbox-allowances baarcha-domains-reconcile baarcha-project-env-apply baarcha-project-supervision baarcha-credit-settle baarcha-media-reconcile baarcha-slack-activity baarcha-email baarcha-chat-memory baarcha-admin-prune baarcha-db-backup baarcha-geoip-update'.split()
installed=pathlib.Path('/opt/baarcha/deploy.sh').read_text()
assert 'for DEPLOY_TIMER in '+' '.join(names)+'; do' in installed
prior=[n+'.timer' for n in names if subprocess.run(['systemctl','is-active','--quiet',n+'.timer']).returncode==0]
(out/'before.json').write_text(json.dumps({'active_timers':prior,'target':sha,'at':time.time()}))
try:
 for timer in prior:subprocess.run(['systemctl','stop',timer],check=True)
 deadline=time.monotonic()+300
 while True:
  busy=[n for n in names if subprocess.check_output(['systemctl','show',n+'.service','-p','ActiveState','--value'],text=True).strip() not in ('inactive','failed')]
  if not busy:break
  assert time.monotonic()<deadline,'Background jobs did not drain: '+','.join(busy)
  time.sleep(2)
 with (out/'deploy.log').open('wb') as log:
  subprocess.run(['/opt/baarcha/deploy.sh',sha],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=1200)
 assert subprocess.check_output(['git','-C','/opt/baarcha/app','rev-parse','HEAD'],text=True).strip()==sha
 subprocess.run(['systemctl','is-active','--quiet','baarcha-landing'],check=True)
 (out/'complete.json').write_text(json.dumps({'complete':True,'revision':sha,'at':time.time()}))
finally:
 failures=[]
 for timer in prior:
  if subprocess.run(['systemctl','start',timer]).returncode:failures.append(timer)
 assert not failures,'Failed to restore timers: '+','.join(failures)
 (out/'timers-restored.json').write_text(json.dumps({'restored':prior,'at':time.time()}))
