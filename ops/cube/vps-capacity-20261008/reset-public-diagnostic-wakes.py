"""Return only the two unmodified diagnostic wakes to their original idle state."""
import datetime,importlib.util,json,os,pathlib,sqlite3,subprocess,time,urllib.request
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'public-diagnostic-reset-02'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ids=['01M415VT76M0M88GDY2NWWE942','01M46NANDW8YVG3Z2F2EXZCY1F'];cutoff=datetime.datetime.fromisoformat('2026-10-09T18:10:10+00:00')
def read(sid):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row
  r=dict(db.execute('select s.id,s.status,s.last_active_at,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,)).fetchone())
  history=[tuple(x) for x in db.execute('select * from task where sandbox_id=? order by task_id',(sid,))]
 return r,history
with b.locked():
 out.mkdir(mode=0o700);scope=json.loads((root/'memory-compaction/fleet-1791568404355196454/scope.json').read_text());before={r['sandbox_id']:r for r in scope};assert all(sid in before for sid in ids)
 env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1];result=[]
 with account_maintenance(ids,out):
  for sid in ids:
   r,history=read(sid);assert r['runtime_id']==before[sid]['runtime_id']
   with sqlite3.connect('file:/var/backups/baarcha-vps-source/20261009T162045Z/controller.PRIVATE.sqlite?mode=ro',uri=True) as db:saved=db.execute('select * from task where sandbox_id=? order by task_id',(sid,)).fetchall()
   if history!=saved or datetime.datetime.fromtimestamp(r['last_active_at'],datetime.timezone.utc)>cutoff:
    result.append({'sandbox_id':sid,'preserved_new_activity':True});continue
   assert (r['status'],r['state'],r['charged']) in [('running','active',1),('stopped','released',0)]
   if r['status']=='running':
    req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/stop',method='POST',headers={'Authorization':'Bearer '+token})
    with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
   after,again=read(sid);assert again==history and (after['status'],after['state'],after['charged'])==('stopped','released',0)
   result.append({'sandbox_id':sid,'original_idle_restored':True})
 b.atomic(out/'complete.json',b.encoded({'complete':True,'results':result,'at':time.time()}));print(json.dumps(result))
