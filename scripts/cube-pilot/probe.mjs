// Disposable in-guest isolation probe, deliberately not a production endpoint.
import http from 'node:http';
import net from 'node:net';
import fs from 'node:fs';
const dial = (host, port) => new Promise(resolve => {
 const s = net.connect({host,port});
 const done = accessible => {s.destroy();resolve({host,port,accessible});};
 s.setTimeout(500,()=>done(false));s.once('connect',()=>done(true));s.once('error',()=>done(false));
});
http.createServer(async(req,res)=>{
 const procTokenReadable=[];
 for(const pid of fs.readdirSync('/proc').filter(n=>/^\d+$/.test(n))){
  try{
   const raw=fs.readFileSync(`/proc/${pid}/environ`);
   if(raw.toString().split('\0').some(v=>v.startsWith('RUNTIMED_HTTP_TOKEN=')&&v.length>'RUNTIMED_HTTP_TOKEN='.length)) procTokenReadable.push(Number(pid));
  }catch{} // EACCES is the expected boundary; never record environment values.
 }
 const status=fs.readFileSync('/proc/self/status','utf8');
 const report={
  supervisorTokenReadableFromProc:procTokenReadable.length>0,
  noNewPrivileges:/^NoNewPrivs:\s+1$/m.test(status),
  effectiveCapabilitiesZero:/^CapEff:\s+0+$/m.test(status),
  supplementaryGroups:process.getgroups(),
  supervisorTokenInherited: !!process.env.RUNTIMED_HTTP_TOKEN,
  hostDockerSocket: fs.existsSync('/var/run/docker.sock'),
  hostCanary: fs.existsSync('/root/cube-pilot/host-only-canary'),
  uid:process.getuid(),
  configuredSecret: process.env.PILOT_SECRET === "owner-secret",
  ipv6Routes:fs.readFileSync('/proc/net/ipv6_route','utf8'),
  networks:await Promise.all([['169.254.169.254',80],['172.17.0.1',3000],['10.0.2.2',22],['1.1.1.1',443],['2606:4700:4700::1111',443]].map(([host,port])=>dial(host,port)))
 };
 fs.writeFileSync('pilot-isolation.json',JSON.stringify(report));
 res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify(report));
}).listen(3002,'127.0.0.1');
// Generate the report so it is retrievable via authenticated workspace API.
setTimeout(()=>http.get('http://127.0.0.1:3002/',r=>r.resume()),1000);
