"""Read-only canonical placement and policy audit; never contacts B200."""
import json,pathlib,sqlite3,subprocess,time,urllib.request
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
 db.row_factory=sqlite3.Row
 def rows(q):return [dict(r) for r in db.execute(q)]
 placement=rows('select a.worker_id,count(*) as count from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id group by a.worker_id')
 pending=rows("select count(*) as count from cube_admission where state='pending'")[0]['count']
 fenced=rows("select count(*) as count from cube_relocation where phase='fenced'")[0]['count']
 active_tasks=rows("select count(*) as count from task where status in ('running','queued')")[0]['count']
 states=rows('select s.status,count(*) as count from sandbox s join runtime_binding b on b.sandbox_id=s.id group by s.status')
 templates=rows('select b.template_id,count(*) as count from runtime_binding b group by b.template_id')
env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env'])
fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);policy=json.loads(env['SANDBOXD_CUBE_ADMISSION'])
assert placement==[{'worker_id':'vps','count':134}] and pending==fenced==active_tasks==0
assert int(env.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
assert all(w['draining'] for w in fleet['workers'] if w['id']!='vps')
with urllib.request.urlopen('http://127.0.0.1:9090/readyz',timeout=5) as r:assert r.read().strip()==b'ready'
result={'passed':True,'placement':placement,'states':states,'profiles':[{'memory_mb':policy['templates'][r['template_id']]['memory_mb'],'count':r['count']} for r in templates],'pending_admissions':pending,'fenced_relocations':fenced,'active_tasks':active_tasks,'coding_queue_enabled':False,'other_workers_drained':True,'memory_budget_mb':policy['resource_budget']['memory_mb'],'cpu_budget_millis':policy['resource_budget']['cpu_millis'],'b200_contacted':False,'at':time.time()}
print(json.dumps(result,indent=2))
