#!/usr/bin/env python3
"""Owned listener controls and read-only inventory. No network reconfiguration."""
import argparse, hashlib, ipaddress, json, os, pathlib, socket, subprocess, threading, time
p=argparse.ArgumentParser();p.add_argument('action',choices=['serve','probe','stats','identity','binding']);p.add_argument('--stage',required=True);p.add_argument('--id');a=p.parse_args();stage=pathlib.Path(a.stage)
spec=json.loads((stage/'scope.json').read_text())
assert spec['worker']=='baarcha-cube-worker-01' and socket.gethostname()==spec['worker']
assert len(spec['marker'])==32 and all(c in '0123456789abcdef' for c in spec['marker'])
binds=[('worker', '10.0.2.15',18081),('gateway','192.168.0.1',18082)]
def interface(name):
 x=json.loads(subprocess.check_output(['ip','-j','-4','addr','show','dev',name]));return x[0]['ifindex'],x[0]['addr_info'][0]['local']
def state():return json.loads((stage/'state/counts.json').read_text())
if a.action=='identity':
 assert interface('enp0s2')[1]==binds[0][1] and interface('cube-dev')[1]==binds[1][1]
 print(json.dumps({'hostname':socket.gethostname(),'boot_id':pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'listeners':binds,'interfaces':{'enp0s2':interface('enp0s2'),'cube-dev':interface('cube-dev')}}))
elif a.action=='binding':
 assert a.id and len(a.id)==32 and all(c in '0123456789abcdef' for c in a.id)
 # Only the two explicitly owned immutable IDs may be requested.
 assert a.id in json.loads((stage/'owned-ids.json').read_text())
 raw=subprocess.check_output(['bpftool','-j','map','dump','pinned','/sys/fs/bpf/ifindex_to_mvmmeta'],timeout=5)
 assert len(raw)<4*1024*1024
 entries=json.loads(raw);found=[]
 for entry in entries:
  # bpftool BTF formatting differs by version; raw mode is requested below
  v=entry.get('value');k=entry.get('key')
  if isinstance(v,list) and isinstance(k,list):
   val=bytes(int(s,16) for s in v);key=bytes(int(s,16) for s in k)
   if val[8:72].split(b'\0')[0].decode('ascii','replace')==a.id:found.append({'id':a.id,'ifindex':int.from_bytes(key,'little'),'address':str(ipaddress.IPv4Address(val[4:8])),'generation':int.from_bytes(val[:4],'little')})
  elif isinstance(v,dict):
   uuid=v.get('uuid',[])
   if isinstance(uuid,list): uuid=bytes(uuid).split(b'\0')[0].decode('ascii','replace')
   if uuid==a.id:
    ip=v['ip'];addr=str(ipaddress.IPv4Address(int(ip).to_bytes(4,'little')))
    found.append({'id':a.id,'ifindex':k,'address':addr,'generation':v['version']})
 assert len(found)==1,'owned binding not uniquely found'
 print(json.dumps(found[0]))
elif a.action=='stats':print(json.dumps(state()))
elif a.action=='probe':
 result=[]
 for label,addr,port in binds:
  with socket.create_connection((addr,port),timeout=3) as s:
   s.settimeout(5);s.sendall(('GET /'+spec['marker']+' HTTP/1.0\r\nHost: synthetic.invalid\r\n\r\n').encode());data=b''
   while len(data)<2048:
    b=s.recv(2048-len(data))
    if not b:break
    data+=b
   assert spec['marker'].encode() in data,'invalid positive control'
  result.append({'listener':label,'marker':True});time.sleep(.26)
 print(json.dumps({'controls':result,'counts':state()}))
else:
 assert os.geteuid()==65534,'listener must be unprivileged'
 lock=threading.Lock();counts={'worker':0,'gateway':0};started=time.monotonic()
 def save():
  tmp=stage/'state/counts.tmp';tmp.write_text(json.dumps(counts));os.replace(tmp,stage/'state/counts.json')
 save()
 def listen(label,addr,port):
  with socket.socket() as srv:
   srv.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);srv.bind((addr,port));srv.listen(4);srv.settimeout(1)
   while time.monotonic()-started<1200:
    try:conn,_=srv.accept()
    except TimeoutError:continue
    with conn:
     with lock:counts[label]+=1;save();assert sum(counts.values())<=256
     conn.settimeout(5)
     try:
      data=conn.recv(1024)
      if data.startswith(('GET /'+spec['marker']+' ').encode()):
       body=json.dumps({'marker':spec['marker']}).encode();conn.sendall(b'HTTP/1.0 200 OK\r\nContent-Length: '+str(len(body)).encode()+b'\r\n\r\n'+body)
     except (TimeoutError,ConnectionError):pass
 threads=[threading.Thread(target=listen,args=b) for b in binds]
 for t in threads:t.start()
 for t in threads:t.join()
