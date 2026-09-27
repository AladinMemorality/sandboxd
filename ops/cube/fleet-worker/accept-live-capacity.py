#!/usr/bin/env python3
"""Measure live migrated projects plus isolated filler pages at fleet capacity.

This is a serving/wake acceptance, not a claim about 100 simultaneous builds.
Existing customer content is never edited. Only journaled operator apps are
deleted. Run after relocation has completed, under the shared operator locks.
"""
import concurrent.futures,hashlib,importlib.util,json,secrets,statistics,sys,threading,time
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urlsplit
ROOT=Path('/opt/baarcha-bench/cube-fleet-20260927/capacity-ready')
s=importlib.util.spec_from_file_location('move',ROOT/'relocation-canary.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
c=m.c

def main():
    with m.r.b.locked():
        assert not c.rows("SELECT id FROM cube_relocation WHERE phase='fenced'")
        job=ROOT/('live-capacity-'+secrets.token_hex(8));job.mkdir(mode=0o700)
        prefix=job.name;owner='operator:capacity-ready';target=100
        baseline=c.rows('SELECT sandbox_id,runtime_id FROM runtime_binding ORDER BY sandbox_id')
        projects=c.rows("SELECT s.id sandbox_id,s.app_id,s.status,s.visibility,a.name,p.worker_id FROM sandbox s JOIN app a ON a.id=s.app_id JOIN runtime_binding b ON b.sandbox_id=s.id JOIN cube_admission p ON p.runtime_id=b.runtime_id WHERE coalesce(a.external_user_id,'') NOT LIKE 'operator:%' AND a.name NOT LIKE 'minecraft-tunnel%' ORDER BY s.id")
        assert sum(row['worker_id']=='b200-01' for row in projects)>=50,'relocate customer projects before acceptance'
        assert sum(row['worker_id']=='vps' for row in projects)<=4,'VPS customer cohort exceeds its running budget'
        c.save(job/'baseline.json',baseline);c.save(job/'projects.json',projects)
        # Temporarily park the explicitly deprioritized TCP-only tunnels so
        # every slot counted in this HTTP test has a page we can verify.
        excluded=c.rows("SELECT s.id sandbox_id,s.status FROM sandbox s JOIN app a ON a.id=s.app_id WHERE a.name LIKE 'minecraft-tunnel%'")
        parked=[]
        apps=[];ready={};guard=threading.Lock();stop=threading.Event();errors=[]
        prior_fixtures=sum(row['sandbox_id']=='01M3D1Q0E1KM1FEM244XVHEC65' for row in projects)
        report=dict(complete=False,profile='migrated customer pages plus private Vite fillers',target=target,web_projects=len(projects),customer_projects=len(projects)-prior_fixtures,prior_operator_fixtures=prior_fixtures)
        def event(phase,**kw):
            c.save(job/'report.json',report);c.save(job/'phase.json',dict(at=time.time(),phase=phase,**kw))
            print(json.dumps(dict(phase=phase,**kw)),flush=True)
        def charged():return c.rows('SELECT worker_id,sum(charged) count FROM cube_admission GROUP BY worker_id ORDER BY worker_id')
        def start(row):
            status,_=c.api('POST','/v1/sandboxes/'+row['sandbox_id']+'/start');assert status==200,'canonical wake failed'
            with guard:ready[row['sandbox_id']]=row
        def preview(row,assets=False):
            sid=row['sandbox_id'];status,v=c.api('POST','/v1/sandboxes/'+sid+'/preview-access');assert status==200
            headers={'Host':urlsplit(v['url']).netloc,'Cookie':'sandbox_preview='+v['token']}
            before=time.monotonic();status,raw=c.request('127.0.0.1',9090,'/',headers=headers,timeout=60)
            found=[]
            if assets and status==200:
                class Parser(HTMLParser):
                    def handle_starttag(self,tag,attrs):
                        a=dict(attrs);url=a.get('src') if tag=='script' else a.get('href') if tag=='link' and a.get('rel')=='stylesheet' else None
                        if url and url.startswith('/') and not url.startswith('//') and len(found)<4:found.append(url)
                Parser().feed(raw.decode('utf-8',errors='replace'))
            codes=[]
            for path in found:
                code,_=c.request('127.0.0.1',9090,path,headers=headers,timeout=60);codes.append(code)
            if row.get('marker'):assert row['marker'].encode() in raw,'fixture identity differs'
            return dict(sandbox_id=sid,status=status,asset_statuses=codes,seconds=time.monotonic()-before,bytes=len(raw))
        def traffic():
            while not stop.wait(20):
                with guard:rows=list(ready.values())
                try:
                    with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
                        observations=list(pool.map(preview,rows))
                    bad=[v for v in observations if v['status']!=200]
                    c.save(job/'traffic.json',dict(at=time.time(),count=len(rows),failures=bad))
                    if bad:errors.append('background page failure');return
                except Exception as e:errors.append(type(e).__name__);return
        thread=threading.Thread(target=traffic,daemon=True);thread.start()
        try:
            for row in excluded:
                if row['status']=='running':
                    assert not c.rows("SELECT task_id FROM task WHERE sandbox_id=? AND status='running'",(row['sandbox_id'],)), 'excluded tunnel has an active task'
                    parked.append(row);c.save(job/'parked.json',parked)
                    status,_=c.api('POST','/v1/sandboxes/'+row['sandbox_id']+'/stop');assert status==200
            # Serial wake batches avoid overwhelming the management link while
            # the traffic thread keeps earlier projects warm.
            for offset in range(0,len(projects),4):
                with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:list(pool.map(start,projects[offset:offset+4]))
                event('customers-started',count=len(ready))
            with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
                observed=list(pool.map(lambda row:preview(row,True),projects))
            c.save(job/'customer-preflight.json',observed)
            assert all(v['status']==200 and all(code==200 for code in v['asset_statuses']) for v in observed),'customer page/assets failed before peak'
            while sum(v['count'] for v in charged())<target:
                assert not errors,'background traffic failed'
                marker=prefix+'-'+str(len(apps))
                status,app=c.api('POST','/v1/apps',dict(name='Cube capacity page '+str(len(apps)),runtime_preset='react-vite',external_user_id=owner,external_project_id=marker,tags=['operator-acceptance',prefix]))
                assert status==201
                apps.append(dict(id=app['id'],marker=marker));c.save(job/'apps.PRIVATE.json',apps)
                status,sb=c.api('POST','/v1/apps/'+app['id']+'/sandbox',dict(runtime_preset='react-vite',ports=[3000]))
                if status!=201:
                    c.save(job/'create-failure.PRIVATE.json',dict(status=status,response=sb));raise RuntimeError('filler creation failed; preserve uncertain reservation for review')
                row=dict(sandbox_id=sb['id'],app_id=app['id'],marker=marker)
                status,_=c.request('127.0.0.1',9090,'/v1/sandboxes/'+sb['id']+'/files?path=index.html','PUT','<html><body>'+marker+'</body></html>',{'Authorization':'Bearer '+c.TOKEN,'Content-Type':'text/html'});assert status==200
                m.preview(sb['id'],marker)
                with guard:ready[sb['id']]=row
                event('filler-ready',total=sum(v['count'] for v in charged()),fillers=len(apps))
            rows=c.rows("SELECT b.sandbox_id,b.runtime_id,a.worker_id FROM runtime_binding b JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE a.charged=1")
            assert len(rows)==target and charged()==[dict(worker_id='b200-01',count=96),dict(worker_id='vps',count=4)]
            def native(row):
                status,raw=c.request('127.0.0.1',20300,'/sandboxes/'+row['runtime_id'],headers={'X-API-Key':c.ENV['SANDBOXD_CUBE_API_KEY']},timeout=60)
                v=json.loads(raw);assert status==200 and v['state']=='running' and v['cpuCount']==2 and v['memoryMB']==2048
                status,raw=c.request('10.254.240.1',18089,'/cube/sandbox/info?sandbox_id='+row['runtime_id']+'&instance_type=cubebox',timeout=60)
                v=json.loads(raw);assert status==200 and v['ret']['ret_code']==200 and len(v['data'])==1
                assert v['data'][0]['host_id']=={'vps':'10.0.2.15','b200-01':'10.254.240.2'}[row['worker_id']]
                return dict(**row,running=True)
            with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:report['native_running']=list(pool.map(native,rows))
            # Include any genuine visitor sessions already running on VPS in
            # the simultaneous page proof; never count an unobserved slot.
            with guard:
                for row in rows:ready.setdefault(row['sandbox_id'],row)
            event('all-running',count=target)
            report['rounds']=[]
            for iteration in range(3):
                with guard:allrows=list(ready.values())
                assert len(allrows)==target
                with concurrent.futures.ThreadPoolExecutor(max_workers=20) as pool:round_=list(pool.map(lambda row:preview(row,True),allrows))
                assert all(v['status']==200 and all(code==200 for code in v['asset_statuses']) for v in round_),'peak page/assets failed'
                report['rounds'].append(round_);assert sum(v['count'] for v in charged())==target
                event('peak-round',round=iteration+1,count=target);time.sleep(30)
            assert not errors
            marker=prefix+'-overflow'
            status,app=c.api('POST','/v1/apps',dict(name='Cube capacity overflow check',runtime_preset='react-vite',external_user_id=owner,external_project_id=marker,tags=['operator-acceptance',prefix]));assert status==201
            apps.append(dict(id=app['id'],marker=marker));c.save(job/'apps.PRIVATE.json',apps)
            status,refusal=c.api('POST','/v1/apps/'+app['id']+'/sandbox',dict(runtime_preset='react-vite',ports=[3000]))
            assert status==503 and refusal['error']['code']=='runtime_capacity','overflow did not stop at admission'
            assert not c.rows('SELECT id FROM sandbox WHERE app_id=?',(app['id'],))
            report['overflow_refused']=True
            stop.set();thread.join(120);assert not thread.is_alive() and not errors
            filler_ids={app['id'] for app in apps}
            selected=next(row for row in rows if row['worker_id']=='b200-01' and ready[row['sandbox_id']].get('app_id') in filler_ids)
            sid=selected['sandbox_id'];status,_=c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
            assert sum(v['count'] for v in charged())==target-1
            began=time.monotonic();status,_=c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200
            report['peak_wake_seconds']=time.monotonic()-began
            assert preview(ready[sid])['status']==200 and sum(v['count'] for v in charged())==target
            times=sorted(v['seconds'] for round_ in report['rounds'] for v in round_)
            report.update(complete=True,fillers=len(apps)-1,page_requests=len(times),p50_seconds=statistics.median(times),p95_seconds=times[int(.95*(len(times)-1))])
            event('acceptance-passed',count=target,p50=report['p50_seconds'],p95=report['p95_seconds'])
        finally:
            stop.set();thread.join(120)
            failures=[]
            for app in reversed(apps):
                try:
                    status,value=c.api('GET','/v1/apps/'+app['id']);assert status==200 and value['external_user_id']==owner and value['external_project_id']==app['marker']
                    status,_=c.api('DELETE','/v1/apps/'+app['id']);assert status==204
                except Exception:failures.append(app['id'])
            for row in projects:
                if row['status']=='stopped' and not c.rows("SELECT task_id FROM task WHERE sandbox_id=? AND status='running'",(row['sandbox_id'],)):
                    status,_=c.api('POST','/v1/sandboxes/'+row['sandbox_id']+'/stop')
                    if status!=200:failures.append(row['sandbox_id'])
            for row in parked:
                status,_=c.api('POST','/v1/sandboxes/'+row['sandbox_id']+'/start')
                if status!=200:failures.append(row['sandbox_id'])
            report['cleanup_failures']=failures
            report['bindings_preserved']=c.rows('SELECT sandbox_id,runtime_id FROM runtime_binding ORDER BY sandbox_id')==baseline
            event('finished',complete=report['complete'],cleanup_failures=len(failures),bindings_preserved=report['bindings_preserved'])
            assert not failures and report['bindings_preserved']
if __name__=='__main__':main()
