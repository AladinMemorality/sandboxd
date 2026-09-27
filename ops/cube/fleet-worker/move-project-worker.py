#!/usr/bin/env python3
"""Private project move transport. All archive I/O stays on the named worker."""
import base64, concurrent.futures, fcntl, hashlib, http.client, json, os, re, shlex, socket, struct, subprocess, sys, time, uuid, zipfile
from pathlib import Path
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

class HTTPFailure(RuntimeError):
    def __init__(self,method,path,status):
        self.operation=method+' '+path;self.status=status

def digest(path,stock=None):
    sha=hashlib.sha256()
    with zipfile.ZipFile(path) as z:
        names=z.namelist();assert len(names)==len(set(names)) and len(names)<=500000
        for name in sorted(names):
            entry=z.getinfo(name);h=hashlib.sha256()
            with z.open(entry) as f:
                while data:=f.read(1024**2):h.update(data)
            if stock and name in stock:
                assert [entry.external_attr,entry.file_size,h.hexdigest()]==stock[name],'destination stock file changed'
                continue
            sha.update(json.dumps([name,entry.external_attr,entry.file_size,h.hexdigest()],separators=(',',':')).encode())
        assert not stock or set(stock)<=set(names)
    return sha.hexdigest()

class Worker:
    def __init__(self,job):
        self.job=job
        if job['worker']=='b200-01':
            assert socket.gethostname()=='baarcha-cube-worker-b200-01'
            self.origin=('10.254.240.2',28080)
            route=json.loads(subprocess.check_output(['ip','-j','route','get',self.origin[0]]))[0]
            assert route['type']=='local' and route['dev']=='lo'
            base=Path('/data/cube-project-moves')
        else:
            assert job['worker']=='vps' and socket.gethostname()=='Ubuntu-noble-latest-amd64-base.zst'
            self.origin=('127.0.0.1',20080);base=Path('/opt/baarcha-bench/cube-fleet-20260927/project-moves')
        assert re.fullmatch('[a-zA-Z0-9_-]{1,128}',job['id'])
        assert re.fullmatch('[a-f0-9]{32}',job['runtime_id'])
        self.root=base/job['id'];self.root.mkdir(mode=0o700,parents=True,exist_ok=True)
        assert not self.root.is_symlink()
        self.headers=job['headers']
        assert set(self.headers)=={'Host','Authorization','cube-traffic-access-token'}
        assert self.headers['Host'].startswith('3031-'+job['runtime_id']+'.')
        assert not any('\r' in v or '\n' in v for v in self.headers.values())

    def http(self,method,path,body=None,size=None,headers=None,export=None):
        c=http.client.HTTPConnection(*self.origin,timeout=600)
        try:
            h=dict(self.headers if headers is None else headers)
            if isinstance(body,(dict,list)):
                body=json.dumps(body).encode();h['Content-Type']='application/json'
            if size is not None:h['Content-Length']=str(size)
            c.request(method,path,body,h);r=c.getresponse()
            if not 200<=r.status<300:raise HTTPFailure(method,path,r.status)
            if export:
                count=0
                with export.open('wb') as out:
                    while data:=r.read(1024**2):
                        count+=len(data);assert count<=4*1024**3;out.write(data)
                    out.flush();os.fsync(out.fileno())
                return
            data=r.read(2*1024**2+1);assert len(data)<=2*1024**2
            return data
        finally:c.close()

    def control(self,method,path,body=None):
        deadline=time.monotonic()+60
        while True:
            try:return json.loads(self.http(method,path,body) or b'{}')
            except (OSError,RuntimeError):
                if time.monotonic()>deadline:raise
                time.sleep(1)

    def inventory(self):
        # Reuse the reviewed owner manifest. The guest rejects unclassified or
        # missing paths, so a changed home fails before any routing switch.
        manifest=self.job['home_manifest']
        assert manifest['version'] in (1,2) and len(json.dumps(manifest))<=32768
        return dict(manifest,version=2)

    def export(self):
        assert not (self.root/'exported.json').exists(),'do not silently reuse an old source snapshot'
        assert self.control('GET','/status')['active_task'] is None
        self.control('POST','/workspace/quiesce')
        home=self.inventory();history={'task_ids':self.job['task_ids']}
        self.http('GET','/export/private-workspace-v2',export=self.root/'workspace.zip')
        self.http('POST','/export/private-home-v2',home,export=self.root/'home.zip')
        self.http('POST','/export/private-task-history',history,export=self.root/'history.zip')
        result={'id':self.job['id'],'sandbox_id':self.job['sandbox_id'],'runtime_id':self.job['runtime_id'],'home_manifest':home,'task_ids':self.job['task_ids'],'artifacts':{}}
        for role in ['workspace','home','history']:
            p=self.root/(role+'.zip')
            info={'sandbox_id':self.job['sandbox_id'],'role':role,'sha256':digest(p),'archive_bytes':p.stat().st_size}
            (self.root/(role+'-manifest.json')).write_text(json.dumps(info));result['artifacts'][role]=info
        (self.root/'exported.json').write_text(json.dumps(result))
        return result

    def fetch(self,role,receipt):
        sha=receipt['sha256'];assert re.fullmatch('[a-f0-9]{64}',sha)
        assert receipt['url'].startswith('https://punicas.s3.eu-central-1.amazonaws.com/baarcha/cube/project-moves/'+sha+'/')
        assert 0<receipt['archive_bytes']<=4*1024**3
        target=self.root/(role+'.zip')
        if target.exists():assert digest(target)==sha;return target
        count=receipt['archive_bytes'];starts=range(0,count,4*1024**2)
        def fetchpart(start):
            end=min(start+4*1024**2,count)-1;p=self.root/(role+'.part-'+str(start))
            result=subprocess.run(['curl','--config','-','--noproxy','*','--fail','--silent','--retry','3','--retry-all-errors','--connect-timeout','10','--max-time','40','--speed-limit','131072','--speed-time','10','--range',f'{start}-{end}','--max-filesize',str(end-start+1),'-o',str(p)],input=('url = '+json.dumps(receipt['url'])+'\n').encode(),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=180)
            assert result.returncode==0 and p.stat().st_size==end-start+1;return p
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:parts=list(pool.map(fetchpart,starts))
        decrypt=Cipher(algorithms.AES(base64.b64decode(receipt['key'])),modes.GCM(base64.b64decode(receipt['iv']),base64.b64decode(receipt['tag']))).decryptor();decrypt.authenticate_additional_data(sha.encode())
        pending=target.with_suffix('.pending')
        with pending.open('wb') as out:
            for p in parts:
                with p.open('rb') as src:
                    while data:=src.read(1024**2):out.write(decrypt.update(data))
            out.write(decrypt.finalize());out.flush();os.fsync(out.fileno())
        assert digest(pending)==sha;os.replace(pending,target)
        for p in parts:p.unlink()
        return target

    def restore(self):
        files={role:self.fetch(role,self.job['receipts'][role]) for role in ['workspace','home','history']}
        assert not (self.root/'restore-intent').exists(),'reconcile interrupted import before retry'
        (self.root/'restore-intent').touch()
        boot=self.control('GET','/status')['runtimed']['booted_at'];self.control('POST','/workspace/quiesce')
        with files['workspace'].open('rb') as src:self.http('PUT','/import/private-workspace-v2',src,files['workspace'].stat().st_size)
        deadline=time.monotonic()+90
        while self.control('GET','/status')['runtimed']['booted_at']==boot:
            assert time.monotonic()<deadline;time.sleep(.5)
        self.control('POST','/workspace/quiesce')
        home=json.dumps(self.job['source']['home_manifest']).encode()
        framed=self.root/'home-framed.pending'
        with framed.open('wb') as out,files['home'].open('rb') as src:
            out.write(struct.pack('>I',len(home))+home)
            while data:=src.read(1024**2):out.write(data)
        with framed.open('rb') as src:self.http('PUT','/import/private-home-v2',src,framed.stat().st_size)
        framed.unlink()
        with files['history'].open('rb') as src:self.http('PUT','/import/private-task-history',src,files['history'].stat().st_size)
        return self.verify()

    def verify(self):
        assert (self.root/'restore-intent').exists()
        self.control('POST','/workspace/quiesce')
        home=json.loads(json.dumps(self.job['source']['home_manifest']));stock={}
        if '.bash_logout' not in [e['path'] for e in home['entries']]:
            # Fresh Cube templates contain this Debian stock file. Older
            # migrated homes do not. Verify its exact contents/mode separately
            # while comparing every source entry without dropping any paths.
            home['entries'].append(dict(path='.bash_logout',disposition='preserve'))
            stock['.bash_logout']=[2175008768,220,'26882b79471c25f945c970f8233d8ce29d54e9d5eedcd2884f88affa84a18f56']
        for role,method,path,body in [('workspace','GET','/export/private-workspace-v2',None),('home','POST','/export/private-home-v2',home),('history','POST','/export/private-task-history',{'task_ids':self.job['source']['task_ids']})]:
            verify=self.root/(role+'.verify.zip');self.http(method,path,body,export=verify)
            assert digest(verify,stock if role=='home' else None)==self.job['receipts'][role]['sha256'],role+' differs after import';verify.unlink()
        revision=self.job['sandbox_id']+':'+str(self.job['config_revision'])
        self.control('POST','/config',{'env':self.job['env'],'revision':revision})
        deadline=time.monotonic()+60
        while self.control('GET','/status').get('app_config_revision')!=revision:
            assert time.monotonic()<deadline;time.sleep(.5)
        self.control('POST','/workspace/resume')
        (self.root/'content-verified.json').write_text(json.dumps({'content_verified':True}))
        print(json.dumps({'stage':'content-verified'}),flush=True)
        headers={**self.headers,'Host':self.headers['Host'].replace('3031-',str(self.job['web_port'])+'-',1)}
        deadline=time.monotonic()+120
        while True:
            try:self.http('GET','/',headers=headers);break
            except (OSError,RuntimeError):
                assert time.monotonic()<deadline,'application did not become ready';time.sleep(1)
        proof={'RelocationID':self.job['id'],'SandboxID':self.job['sandbox_id'],'RuntimeID':self.job['runtime_id'],'WorkerID':self.job['worker'],'ConfigApplied':True,'ApplicationReady':True}
        proof['DestinationStockFiles']=stock
        for role in ['workspace','home','history']:
            proof[role.title()+'SHA256']=self.job['receipts'][role]['sha256'];proof[role.title()+'Verified']=True
        (self.root/'verified.json').write_text(json.dumps(proof));return proof

def main():
    assert os.geteuid()==0;os.umask(0o077)
    raw=sys.stdin.buffer.read(256*1024+1);assert len(raw)<=256*1024
    job=json.loads(raw);w=Worker(job)
    with (w.root/'operator.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        result={'export':w.export,'restore':w.restore,'verify':w.verify}[job['action']]()
    print(json.dumps(result),flush=True)

if __name__=='__main__':
    try:main()
    except Exception as e:
        import traceback
        print(json.dumps({'failed':True,'error':type(e).__name__,'line':traceback.extract_tb(e.__traceback__)[-1].lineno,'operation':getattr(e,'operation',None),'status':getattr(e,'status',None)}),flush=True);sys.exit(1)
