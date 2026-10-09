"""Repeat source-restored app wake checks, then settle the failed density barrier."""
import contextlib,importlib.util,json,os,pathlib,sqlite3,subprocess,sys,time,urllib.request
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');stage=root/'exited-recovery-01';sid='01M24GEQ88YGHHH3WPC9WD9DY0'
sys.path.insert(0,str(root));from maintenance_account import account_maintenance
from migration_lifecycle import lifecycle
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
def rows(q,args=()):
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:return db.execute(q,args).fetchall()
(stage/'wake-maintenance').mkdir(mode=0o700)
with b.locked(),account_maintenance([sid],stage/'wake-maintenance'):
 assert json.loads((stage/'complete.json').read_text())['restored']
 release=json.loads((root/'stop-state-release-eda67d2/deployed.json').read_text());assert release['deployed']
 assert rows("select task_id from task where status in ('running','queued')")==[]
 x=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];assert x['Image']==release['image']
 env=dict(x.split('=',1) for x in x['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
 def api(action):
  req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
  with lifecycle():
   with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
 namespace={};source=(stage/'restore.py').read_text();exec(compile(source[:source.index('request=json.loads')],str(stage/'restore.py'),'exec'),namespace)
 worker=namespace['LocalWorker'](json.loads((stage/'worker-job.PRIVATE.json').read_text()))
 api('start');worker.application_checks();api('stop');times=[]
 for cycle in range(3):
  began=time.monotonic();api('start');times.append(time.monotonic()-began);worker.application_checks();api('stop')
  assert rows('select s.status,a.state,a.charged from sandbox s join runtime_binding r on r.sandbox_id=s.id join cube_admission a on a.runtime_id=r.runtime_id where s.id=?',(sid,))==[('stopped','released',0)]
 barrier=root/'restore-barrier.json';assert json.loads(barrier.read_text())=={'purpose':'reviewed-restore-barrier','allowed_sandboxes':[],'owner':'real-preview-density-50-balanced-03'}
 failed=root/'real-preview-density-50-balanced-03';cleanup=json.loads((failed/'cleanup.json').read_text())
 assert [r['sandbox_id'] for r in cleanup['runtimes'] if not r.get('stopped') and not r.get('preserved_for_user_work')]==[sid]
 assert not rows("select admission_key from cube_admission where state='pending'")
 result={'passed':True,'sandbox_id':sid,'runtime_id':worker.job['runtime_id'],'wake_seconds':times,'cycles':3,'original_retained':True,'previous_cleanup_resolved':True,'model_calls':False,'b200_contacted':False,'at':time.time()}
 b.atomic(stage/'wake-check.json',b.encoded(result));b.atomic(failed/'cleanup-reconciliation.json',b.encoded(result));barrier.unlink();print(json.dumps(result),flush=True)
