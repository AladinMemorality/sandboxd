"""Read-only canonical placement and policy audit; never contacts B200."""
import collections,json,os,pathlib,sqlite3,subprocess,time,urllib.request
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
 db.row_factory=sqlite3.Row
 def rows(q):return [dict(r) for r in db.execute(q)]
 bindings=rows('select sandbox_id,runtime_id,template_id from runtime_binding')
 binding_map={r['sandbox_id']:r['runtime_id'] for r in bindings}
 binding_templates={r['sandbox_id']:r['template_id'] for r in bindings}
 placement=rows('select a.worker_id,count(*) as count from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id group by a.worker_id')
 pending=rows("select count(*) as count from cube_admission where state='pending'")[0]['count']
 fenced=rows("select count(*) as count from cube_relocation where phase='fenced'")[0]['count']
 active_tasks=rows("select count(*) as count from task where status in ('running','queued')")[0]['count']
 states=rows('select s.status,count(*) as count from sandbox s join runtime_binding b on b.sandbox_id=s.id group by s.status')
 templates=rows('select b.template_id,count(*) as count from runtime_binding b group by b.template_id')
container=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
env=dict(x.split('=',1) for x in container['Config']['Env'])
fleet=json.loads(env['SANDBOXD_CUBE_FLEET']);policy=json.loads(env['SANDBOXD_CUBE_ADMISSION'])
assert len(bindings)>=135 and placement==[{'worker_id':'vps','count':len(bindings)}] and pending==fenced==active_tasks==0
assert int(env.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
assert all(w['draining'] for w in fleet['workers'] if w['id']!='vps')
with urllib.request.urlopen('http://127.0.0.1:9090/readyz',timeout=5) as r:assert r.read().strip()==b'ready'
def proof(name):return json.loads((root/name).read_text())
assert proof('finish-vps-recovery-20/complete.json')['complete']
assert proof('resume-retry-release-d5b07bb/deployed.json')['deleted_storage_grant_reconciled']
deployed=proof('stop-state-release-eda67d2/deployed.json');assert deployed['deployed'] and deployed['image']==container['Image'] and deployed['stop_state_verified']
native=proof('full-pause-release-20261009/deployed.json');assert native['deployed'] and native['full_pause_snapshot_policy']
assert proof('full-pause-canary-01/complete.json')['passed']
density=proof('real-preview-density-50-balanced-06/result.json');cleanup=proof('real-preview-density-50-balanced-06/cleanup.json')
assert density['passed'] and density['concurrent_running']==50 and density['http_checks']==600
assert cleanup['complete'] and cleanup['bindings_preserved'] and cleanup['existing_running_preserved']
density_scope=proof('real-preview-density-50-balanced-06/scope.json');before=density_scope['before']
cohort_limits=collections.Counter(policy['templates'][binding_templates[sid]]['memory_mb'] for sid in density_scope['selected']+density_scope['existing_running'])
assert sum(cohort_limits.values())==50
pacing=proof('transfer-readiness-watch-01/result.json');assert pacing['passed'] and pacing['checks']==60 and pacing['failed_checks']==0
assert density['after']['oom_kill']==before['oom_kill'] and density['after']['worker']['oom_kill']==before['worker']['oom_kill']
backuproot=pathlib.Path('/var/backups/baarcha-vps-source');backup=json.loads((backuproot/'latest.json').read_text())
assert backup['verified'] and backup['sandboxes']==len(bindings) and backup['other_worker_bindings']==0 and backup['generated_pnpm_caches_excluded']
assert 0<=time.time()-backup['completed_at']<6*3600
saved=json.loads((backuproot/backup['generation']/'scope.json').read_text())
assert saved['worker']=='vps' and {r['sandbox_id']:r['runtime_id'] for r in saved['bindings']}==binding_map
rollout=proof('supervisor-rollout-2c7e700/complete.json');assert rollout['complete'] and rollout['revision']=='2c7e700' and len(rollout['results'])==len(bindings)
assert {r['sandbox_id']:r['runtime_id'] for r in rollout['results']}==binding_map
archives=proof('cold-archive-move/complete.json');assert archives['complete'] and archives['all_data_preserved']
growth=proof('data-growth-784-complete.json');assert growth['target_bytes']==784*1024**3
st=pathlib.Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2').stat();assert st.st_ino==3932163
fs=os.statvfs('/mnt/nvme');worst_headroom=fs.f_bavail*fs.f_frsize-max(0,growth['target_bytes']-st.st_blocks*512)
assert worst_headroom>128*1024**3
counts=collections.Counter()
for row in templates:counts[policy['templates'][row['template_id']]['memory_mb']]+=row['count']
result={'passed':True,'placement':placement,'states':states,'profiles':[{'memory_mb':memory,'count':count} for memory,count in sorted(counts.items())],'pending_admissions':pending,'fenced_relocations':fenced,'active_tasks':active_tasks,'coding_queue_enabled':False,'other_workers_drained':True,'memory_budget_mb':policy['resource_budget']['memory_mb'],'cpu_budget_millis':policy['resource_budget']['cpu_millis'],'b200_contacted':False,'controller_revision':deployed['revision'],'backup':backup,'supervisor_revision':rollout['revision'],'density':{k:density[k] for k in ['concurrent_running','http_checks','module_http_checks','http_p95_seconds','http_max_seconds','total_pss_bytes','median_pss_bytes','max_pss_bytes','application_scope']},'host_headroom_if_data_disk_full_bytes':worst_headroom,'cold_archive_nvme_bytes_reclaimed':archives['nvme_bytes_reclaimed'],'at':time.time()}
result['density']['guest_limits']=[{'memory_mb':memory,'count':count} for memory,count in sorted(cohort_limits.items())]
result['density']['reserved_memory_mb']=density_scope['reserved_memory_mb']
result['transfer_readiness']=pacing
print(json.dumps(result,indent=2))
