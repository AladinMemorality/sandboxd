#!/usr/bin/env python3
"""Run crafted Ethernet packets through the actual attached guest TC program.

Uses BPF_PROG_TEST_RUN with the real guest interface/map context. It does not
transmit packets to a protected destination; redirect verdicts are failures.
Ordinary guest traffic and witness captures establish real attachment separately.
"""
import argparse, json, pathlib, socket, struct, subprocess, tempfile

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--guest',type=pathlib.Path,required=True)
p.add_argument('--output',type=pathlib.Path,required=True)
args=p.parse_args()
sid=json.loads(args.guest.read_text())['sandbox_id']
dump=json.loads(subprocess.check_output(['bpftool','-j','map','dump','pinned','/sys/fs/bpf/ifindex_to_mvmmeta']))
matches=[x['formatted'] for x in dump if bytes(x['formatted']['value']['uuid']).split(b'\0')[0].decode()==sid]
assert len(matches)==1
ifindex=matches[0]['key']
attached=json.loads(subprocess.check_output(['bpftool','-j','net','show']))[0]['tc']
entry=next(x for x in attached if x['ifindex']==ifindex and x['kind']=='clsact/ingress')
assert entry['name']=='from_cube'
program=entry['id']

def checksum(b):
    if len(b)%2:b+=b'\0'
    total=sum(struct.unpack('!%dH'%(len(b)//2),b))
    total=(total&65535)+(total>>16);total=(total&65535)+(total>>16)
    return (~total)&65535

def packet(dst,protocol=6,source_port=34001,flags=2,fragment=0,ether=0x800):
    source=socket.inet_aton('169.254.68.6');dest=socket.inet_aton(dst)
    if protocol==6:
        payload=struct.pack('!HHIIBBHHH',source_port,18081,1,1,80,flags,8192,0,0)
        pseudo=source+dest+struct.pack('!BBH',0,protocol,len(payload))
        payload=payload[:16]+struct.pack('!H',checksum(pseudo+payload))+payload[18:]
    elif protocol==17:payload=struct.pack('!HHHH',source_port,18082,8,0)
    else:payload=b'\x00'*32
    header=struct.pack('!BBHHHBBH4s4s',69,0,20+len(payload),431,fragment,64,protocol,0,source,dest)
    header=header[:10]+struct.pack('!H',checksum(header))+header[12:]
    return bytes.fromhex('20906fcfcfcf20906ffcfcfc')+struct.pack('!H',ether)+header+payload

report={'program_id':program,'interface':ifindex,'method':'BPF_PROG_TEST_RUN of actually attached program with live maps; no real packet transmission','cases':[]}
with tempfile.TemporaryDirectory(prefix='cube-packet-') as temp:
    root=pathlib.Path(temp)
    # Only ingress_ifindex/ifindex are provided; packet protocol is derived by kernel.
    ctx=bytearray(192);struct.pack_into('<II',ctx,36,ifindex,ifindex)
    (root/'ctx').write_bytes(ctx)
    cases=[]
    for dest in ['10.0.2.15','169.254.68.5','169.254.169.254','65.108.225.153','192.168.1.247']:
        for port in [34001,3000,3031,49983]:
            for flags,label in [(2,'syn'),(16,'ack'),(18,'synack'),(4,'rst')]:
                cases.append((dest+'-'+str(port)+'-'+label,packet(dest,source_port=port,flags=flags)))
    for fragment in [0x2000,1]:cases.append(('fragment-'+str(fragment),packet('10.0.2.15',fragment=fragment)))
    for protocol in [4,41,47,50]:cases.append(('protocol-'+str(protocol),packet('10.0.2.15',protocol=protocol)))
    cases.append(('native-ipv6',packet('10.0.2.15',ether=0x86dd)))
    for name,data in cases:
        (root/'packet').write_bytes(data)
        result=subprocess.run(['bpftool','-j','prog','run','id',str(program),'data_in',str(root/'packet'),'data_out',str(root/'packet-out'),'ctx_in',str(root/'ctx')],capture_output=True,text=True)
        if result.returncode:raise RuntimeError(result.stderr)
        out=json.loads(result.stdout)
        returned=(root/'packet-out').read_bytes()
        reset_to_guest=len(returned)>=54 and returned[30:34]==socket.inet_aton('169.254.68.6') and bool(returned[47]&4)
        report['cases'].append({'name':name,'return_value':out['retval'],'dropped':out['retval']==2 or (out['retval']==7 and reset_to_guest),'reset_to_guest':reset_to_guest})
report['passed']=all(x['dropped'] for x in report['cases'])
args.output.write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps({'program_id':program,'cases':len(report['cases']),'failed':[x for x in report['cases'] if not x['dropped']]}))
raise SystemExit(0 if report['passed'] else 1)
