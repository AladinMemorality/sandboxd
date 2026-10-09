#!/usr/bin/env python3
"""Apply the reviewed supervisor to managed idle guests as they become active."""
import argparse, fcntl, hashlib, json, os, pathlib, pty, re, select, subprocess, time, uuid
from guest import require_static_supervisor
P=pathlib.Path;ROOT=P('/opt/baarcha-published-runtime')

def acquire_update_lock(lock, timeout=180):
    deadline=time.monotonic()+timeout
    while True:
        try:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            return True
        except BlockingIOError:
            if time.monotonic()>=deadline:return False
            time.sleep(min(.25,max(0,deadline-time.monotonic())))

def execute(cid,guest,config,binary,timeout=240):
    master,slave=pty.openpty();os.set_blocking(master,False)
    args=['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','exec','--tty','--exec-id','published-'+uuid.uuid4().hex,'--user','0',cid,'/usr/bin/python3','-c',guest,config]
    proc=subprocess.Popen(args,stdin=slave,stdout=slave,stderr=slave);os.close(slave)
    output=b'';sent=0;ready=False;deadline=time.monotonic()+timeout
    try:
        while time.monotonic()<deadline:
            readers,writers,_=select.select([master],[master] if ready and sent<len(binary) else [],[],.2)
            if readers:
                try:data=os.read(master,65536)
                except BlockingIOError:data=None
                except OSError:break
                if data==b'':break
                if data is None:continue
                output+=data
                if len(output)>65536:raise RuntimeError('guest output limit')
                ready=b'RUNTIME_READY' in output
            if writers:
                try:sent+=os.write(master,binary[sent:sent+16384])
                except BlockingIOError:pass
                except OSError:break
            if proc.poll() is not None and not readers:break
        receipts=[line.split('RUNTIME_RECEIPT=',1)[1] for line in output.decode(errors='replace').splitlines() if line.startswith('RUNTIME_RECEIPT=')]
        if len(receipts)!=1:raise RuntimeError('missing guest receipt')
        receipt=json.loads(receipts[0]);receipt['runtime_id']=cid
        return receipt
    finally:
        os.close(master)
        if proc.poll() is None:proc.terminate()
        try:proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill();proc.wait(timeout=5)

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--probe',action='store_true');parser.add_argument('--container');args=parser.parse_args()
    with open(ROOT/'update.lock','a+') as lock:
        if not acquire_update_lock(lock,180 if args.container else 0):
            print(json.dumps({'runtime_id':args.container,'status':'busy','reason':'update_lock_timeout'}),flush=True)
            return
        guest=(ROOT/'guest.py').read_text();binary=b'';config='probe'
        if not args.probe:
            cfg=json.loads((ROOT/'release.json').read_text());binary=(ROOT/'runtimed').read_bytes()
            assert hashlib.sha256(binary).hexdigest()==cfg['sha256'] and len(binary)==cfg['bytes']
            require_static_supervisor(binary)
            config=json.dumps(cfg)
        ids=[args.container] if args.container else subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list','-q'],stderr=subprocess.DEVNULL,text=True,timeout=30).split()
        for cid in ids:
            if not re.fullmatch('[a-f0-9]{32}',cid):continue
            try:print(json.dumps(execute(cid,guest,config,binary)),flush=True)
            except Exception as e:print(json.dumps({'runtime_id':cid,'status':'failed','error_type':type(e).__name__}),flush=True)

if __name__=='__main__':main()
