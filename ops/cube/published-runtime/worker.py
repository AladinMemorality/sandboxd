#!/usr/bin/env python3
"""Apply the reviewed supervisor to managed idle guests as they become active."""
import argparse, fcntl, hashlib, json, os, pathlib, pty, re, select, subprocess, time, uuid
P=pathlib.Path;ROOT=P('/opt/baarcha-published-runtime')

def execute(cid,guest,config,binary):
    master,slave=pty.openpty()
    args=['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','exec','--tty','--exec-id','published-'+uuid.uuid4().hex,'--user','0',cid,'/usr/bin/python3','-c',guest,config]
    proc=subprocess.Popen(args,stdin=slave,stdout=slave,stderr=slave);os.close(slave)
    output=b'';sent=0;ready=False;deadline=time.monotonic()+240
    try:
        while time.monotonic()<deadline:
            readers,writers,_=select.select([master],[master] if ready and sent<len(binary) else [],[],.2)
            if readers:
                try:data=os.read(master,65536)
                except OSError:break
                if not data:break
                output+=data
                if len(output)>65536:raise RuntimeError('guest output limit')
                ready=b'RUNTIME_READY' in output
            if writers:sent+=os.write(master,binary[sent:sent+16384])
            if proc.poll() is not None and not readers:break
        receipts=[line.split('RUNTIME_RECEIPT=',1)[1] for line in output.decode(errors='replace').splitlines() if line.startswith('RUNTIME_RECEIPT=')]
        if len(receipts)!=1:raise RuntimeError('missing guest receipt')
        receipt=json.loads(receipts[0]);receipt['runtime_id']=cid
        return receipt
    finally:
        os.close(master)
        if proc.poll() is None:proc.terminate()
        proc.wait(timeout=5)

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--probe',action='store_true');parser.add_argument('--container');args=parser.parse_args()
    with open(ROOT/'update.lock','a+') as lock:
        try:fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:return
        guest=(ROOT/'guest.py').read_text();binary=b'';config='probe'
        if not args.probe:
            cfg=json.loads((ROOT/'release.json').read_text());binary=(ROOT/'runtimed').read_bytes()
            assert hashlib.sha256(binary).hexdigest()==cfg['sha256'] and len(binary)==cfg['bytes']
            config=json.dumps(cfg)
        ids=[args.container] if args.container else subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list','-q'],stderr=subprocess.DEVNULL,text=True).split()
        for cid in ids:
            if not re.fullmatch('[a-f0-9]{32}',cid):continue
            try:print(json.dumps(execute(cid,guest,config,binary)),flush=True)
            except Exception as e:print(json.dumps({'runtime_id':cid,'status':'failed','error_type':type(e).__name__}),flush=True)

if __name__=='__main__':main()
