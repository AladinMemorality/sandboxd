#!/usr/bin/env python3
"""Fifty real private apps through the canonical controller during its route fence.

The maintenance parent owns deployment/operator locks and retains the fence on
failure. This script deletes only app IDs acknowledged in its private journal.
It measures concurrent lightweight Vite pages, not fifty saturated build jobs.
"""
from concurrent.futures import ThreadPoolExecutor
from http.client import HTTPConnection
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
import sys
import time
import threading
from urllib.parse import urlsplit


def main():
    assert os.geteuid()==0
    os.umask(0o077)
    parent=Path(sys.argv[1])
    assert parent.parent==Path('/opt/baarcha-cube/worker-01/maintenance')
    job=parent/'fleet-acceptance';job.mkdir(mode=0o700)
    def http(port,path,method='GET',body=None,headers=None,host='127.0.0.1'):
        c=HTTPConnection(host,port,timeout=90)
        try:
            c.request(method,path,body,headers or {});r=c.getresponse();raw=r.read(2*1024**2+1)
            assert len(raw)<=2*1024**2
            return r.status,raw
        finally:c.close()
    status,raw=http(2019,'/config/');assert status==200
    assert json.loads(raw)==json.loads((parent/'offline.json').read_text()),'maintenance routing fence required'
    cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
    env=dict(v.split('=',1) for v in cp['Config']['Env'])
    fleet=json.loads(env['SANDBOXD_CUBE_FLEET'])
    assert [(v['id'],v['admission']['max_active'],v.get('draining',False)) for v in fleet['workers']]==[('vps',4,False),('b200-01',46,False)]
    token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
    headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'}
    def api(method,path,body=None,raw=False):
        status,data=http(9090,path,method,body if raw else None if body is None else json.dumps(body),headers)
        return status,json.loads(data) if data else None
    def dbrows(sql,args=()):
        with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=3) as db:
            return db.execute(sql,args).fetchall()
    assert dbrows('SELECT coalesce(sum(charged),0) FROM cube_admission')==[(0,)],'empty active admission baseline required'
    baseline=dbrows('SELECT id FROM sandbox ORDER BY id')
    prefix='fleet-50-'+secrets.token_hex(8)
    apps=[];report={'complete':False,'profile':'50 concurrent lightweight private Vite pages','prefix':prefix,'guests':[]}
    def save(name,value):
        temp=job/(name+'.pending');temp.write_text(json.dumps(value));os.replace(temp,job/name)
    def event(phase,**values):
        save('result.json',report);save('phase.json',dict(phase=phase,**values));print(json.dumps(dict(phase=phase,**values)),flush=True)
    def new_app(i):
        key=prefix+'-'+str(i);save('create-intent.PRIVATE.json',{'external_project_id':key,'acknowledged_apps':apps})
        status,a=api('POST','/v1/apps',{'name':'Cube fleet capacity acceptance '+str(i),'external_user_id':'operator:fleet-acceptance',
                     'external_project_id':key,'runtime_preset':'react-vite','tags':['operator-acceptance',prefix]})
        assert status==201,'app creation failed'
        apps.append({'id':a['id'],'external_project_id':key});save('apps.PRIVATE.json',apps)
        return a['id']
    def preview(row, startup=False):
        status,v=api('POST','/v1/sandboxes/'+row['sandbox_id']+'/preview-access');assert status==200
        began=time.monotonic()
        while True:
            status,raw=http(9090,'/',headers={'Host':urlsplit(v['url']).netloc,'Cookie':'sandbox_preview='+v['token']})
            if status==200 and (prefix+' '+row['app_id']).encode() in raw:break
            assert startup and time.monotonic()-began<60,'distinct private preview failed (HTTP '+str(status)+')'
            time.sleep(1)
        return {'sandbox_id':row['sandbox_id'],'status':status,'seconds':time.monotonic()-began}
    def allocation():
        counts=dbrows('SELECT worker_id,sum(charged) FROM cube_admission GROUP BY worker_id ORDER BY worker_id')
        assert counts==[('b200-01',46),('vps',4)],'charged fleet differs from 4+46'
        rows=dbrows("SELECT b.sandbox_id,b.runtime_id,a.worker_id FROM runtime_binding b JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE a.charged=1")
        assert len(rows)==50 and {v[0] for v in rows}=={v['sandbox_id'] for v in report['guests']}
        return counts
    def live_proof():
        rows=dbrows("SELECT b.runtime_id,a.worker_id FROM runtime_binding b JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE a.charged=1")
        assert len(rows)==50
        def verify(row):
            ident,worker=row
            status,raw=http(20300,'/sandboxes/'+ident,headers={'X-API-Key':env['SANDBOXD_CUBE_API_KEY']})
            v=json.loads(raw)
            assert status==200 and v['sandboxID']==ident and v['state']=='running' and v['cpuCount']==2 and v['memoryMB']==2048
            status,raw=http(18089,'/cube/sandbox/info?sandbox_id='+ident+'&instance_type=cubebox',host='10.254.240.1')
            v=json.loads(raw);assert status==200 and v['ret']['ret_code']==200 and len(v['data'])==1
            assert v['data'][0]['host_id']=={'vps':'10.0.2.15','b200-01':'10.254.240.2'}[worker]
            return {'runtime_id':ident,'worker':worker,'running':True}
        with ThreadPoolExecutor(max_workers=10) as pool:return list(pool.map(verify,rows))
    stop_traffic=threading.Event();ready_rows=[];traffic_failures=[];traffic_rounds=[]
    def traffic():
        while not stop_traffic.wait(10):
            try:
                rows=list(ready_rows)
                with ThreadPoolExecutor(max_workers=10) as pool:
                    responses=list(pool.map(preview,rows))
                traffic_rounds.append(len(responses))
            except Exception as error:
                traffic_failures.append(type(error).__name__+': '+str(error)[:200]);return
    traffic_thread=threading.Thread(target=traffic,daemon=True)
    traffic_thread.start()
    try:
        for i in range(50):
            assert not traffic_failures,'continuous visitor traffic failed'
            app=new_app(i);began=time.monotonic()
            status,s=api('POST','/v1/apps/'+app+'/sandbox',{'runtime_preset':'react-vite','ports':[3000]})
            if status!=201:
                save('failed-create.PRIVATE.json',{'app_id':app,'response':s,'status':status})
                raise AssertionError('sandbox create failed; inspect private evidence')
            row={'app_id':app,'sandbox_id':s['id'],'create_seconds':time.monotonic()-began}
            report['guests'].append(row);save('result.json',report)
            status,_=api('PUT','/v1/sandboxes/'+s['id']+'/files?path=index.html',
                         '<html><body>'+prefix+' '+app+'</body></html>',raw=True)
            assert status==200,'fixture page write failed'
            row['preview_ready']=preview(row,startup=True)
            ready_rows.append(row)
            event('created',count=i+1)
        assert not traffic_failures,'continuous visitor traffic failed'
        report['allocation']=allocation()
        report['live_runtimes']=live_proof()
        event('all-running',count=50)
        extra=new_app(50)
        status,refusal=api('POST','/v1/apps/'+extra+'/sandbox',{'runtime_preset':'react-vite','ports':[3000]})
        assert status==503 and refusal['error']['code']=='runtime_capacity','51st create was not refused at admission'
        assert dbrows('SELECT count(*) FROM sandbox WHERE app_id=?',(extra,))==[(0,)]
        report['overflow_refused']=True;allocation()
        report['preview_rounds']=[]
        for iteration in range(3):
            with ThreadPoolExecutor(max_workers=10) as pool:
                report['preview_rounds'].append(list(pool.map(preview,report['guests'])))
            allocation();event('preview-round',round=iteration+1,count=50)
            time.sleep(10)
        stop_traffic.set();traffic_thread.join(timeout=100)
        assert not traffic_thread.is_alive() and not traffic_failures,'visitor traffic did not finish cleanly'
        report['background_page_requests']=sum(traffic_rounds)
        # Exercise canonical pause/wake on B200 without losing the private page.
        selected=report['guests'][-1]
        status,_=api('POST','/v1/sandboxes/'+selected['sandbox_id']+'/stop');assert status==200
        assert dbrows("SELECT sum(charged) FROM cube_admission WHERE worker_id='b200-01'")==[(45,)]
        began=time.monotonic();status,_=api('POST','/v1/sandboxes/'+selected['sandbox_id']+'/start');assert status==200
        report['wake_seconds']=time.monotonic()-began;preview(selected);allocation()
        report['live_after_wake']=live_proof()
        report['complete']=True;event('acceptance-passed',count=50)
    except Exception as error:
        report['failure']=str(error)[:256]
        raise
    finally:
        stop_traffic.set();traffic_thread.join(timeout=100)
        report['background_traffic_failures']=traffic_failures
        event('cleanup-started',acknowledged_apps=len(apps))
        failures=[]
        for app in reversed(apps):
            try:
                status,a=api('GET','/v1/apps/'+app['id'])
                assert status==200 and a['external_user_id']=='operator:fleet-acceptance' and a['external_project_id']==app['external_project_id']
                status,_=api('DELETE','/v1/apps/'+app['id']);assert status==204
            except Exception:
                failures.append(app['id'])
        report['cleanup_failures']=failures
        report['cleanup_verified']=not failures and dbrows('SELECT id FROM sandbox ORDER BY id')==baseline and dbrows('SELECT coalesce(sum(charged),0) FROM cube_admission')==[(0,)]
        event('finished',complete=report['complete'],cleanup_verified=report['cleanup_verified'])
        assert report['cleanup_verified'],'owned fixture cleanup needs review'


if __name__=='__main__':
    try:main()
    except Exception as error:raise SystemExit('Fleet acceptance requires review: '+type(error).__name__)
