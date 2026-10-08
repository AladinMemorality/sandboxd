"""Summarize isolated VPS build concurrency without exporting credentials."""
import json,pathlib,math
R=pathlib.Path('/opt/baarcha/operations/vps-build-concurrency-20261008-01')
def read(n):return json.loads((R/n).read_text())
def pct(xs,p):return sorted(xs)[max(0,math.ceil(len(xs)*p)-1)] if xs else None
report=read('result.json');metrics=[json.loads(s) for s in (R/'metrics.jsonl').read_text().splitlines()]
rounds=[]
for cohort in report['build_rounds']:
 guests=cohort['guests'];durations=[g['seconds'] for g in guests if 'seconds' in g]
 events=[]
 for g in guests:
  if 'seconds' in g and 'started' in g:events.extend([(g['started']/1000,1),(g['started']/1000+g['seconds'],-1)])
 active=peak=0
 for _,delta in sorted(events):active+=delta;peak=max(peak,active)
 during=[m for m in metrics if events and min(t for t,d in events)<=m['at']<=max(t for t,d in events)]
 rounds.append({'requested':cohort['concurrent'],'peak_overlapping_builds':peak,'passed':sum(g['state']=='passed' for g in guests),'cohort_seconds':cohort['seconds'],'build_p50_seconds':pct(durations,.5),'build_p95_seconds':pct(durations,.95),'build_max_seconds':max(durations,default=None),'jobs_per_minute':60*len(guests)/cohort['seconds'],'host_min_available_gib':min((m['outer']['available']/1024**3 for m in during),default=None),'worker_min_available_gib':min((m['worker']['available']/1024**3 for m in during),default=None),'platform_probe_max_seconds':max((p['seconds'] for m in during for p in m['probes']),default=None)})
checks=[r for group in report['rounds'] for r in group]
baseline=read('baseline.json');after=read('after.json');verify=read('final-verification.json')
summary={'completed':report.get('completed',False),'cleanup_verified':report['cleanup_verified'],'created_fixtures':report['created_fixtures'],'profile':'50 copies of the React/Vite starter, 512MiB each; npm run build','b200_contacted':False,'build_cohorts':rounds,'http_checks':len(checks),'http_failures':sum(not r['ok'] for r in checks),'http_p95_seconds':pct([r['seconds'] for r in checks],.95),'http_max_seconds':max(r['seconds'] for r in checks),'host_min_available_gib':min(m['outer']['available']/1024**3 for m in metrics),'worker_min_available_gib':min(m['worker']['available']/1024**3 for m in metrics),'oom_delta':{host:after[host]['oom']-baseline[host]['oom'] for host in ['outer','worker']},'verification':verify}
if (R/'cloud-concurrency.json').exists():
 c=read('cloud-concurrency.json');summary['cloud_tool_calls']={k:v for k,v in c.items() if k!='results'};summary['cloud_tool_calls']['p95_seconds']=pct([x['seconds'] for x in c['results']],.95)
(R/'summary.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary,indent=2))
