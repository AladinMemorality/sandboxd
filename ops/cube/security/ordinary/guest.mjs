// Synthetic owned app only. No shell, model calls, credentials or raw sockets.
import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
const spec = JSON.parse(fs.readFileSync(new URL('./config/fixture.json', import.meta.url)));
if (!/^[a-f0-9]{32}$/.test(spec.marker)) throw Error('invalid fixture marker');
let accepts = 0, attempts = 0, busy = false, lastStart = 0;
const started = Date.now();
const sentinel = http.createServer((req,res) => {
  if (req.url !== '/'+spec.marker || req.method !== 'GET') {res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type','application/json');res.setHeader('Connection','close');res.end(JSON.stringify({marker:spec.marker}));
});
sentinel.maxHeadersCount = 12; sentinel.headersTimeout=5000; sentinel.requestTimeout=5000;
sentinel.on('connection',s=>{accepts++;s.setTimeout(5000,()=>s.destroy());});
sentinel.listen(3005,'0.0.0.0');
function identity() {
 const status=fs.readFileSync('/proc/self/status','utf8');
 const selected={};for(const name of ['Uid','Gid','CapInh','CapPrm','CapEff','CapBnd','CapAmb','NoNewPrivs']) selected[name]=status.match(new RegExp('^'+name+':\\s*(.*)$','m'))?.[1];
 const forbiddenPaths=['/var/run/docker.sock','/run/docker.sock','/root/cube-production','/opt/baarcha/landing.env','/usr/local/services/cubetoolbox/.one-click.env'];
 const accessible=forbiddenPaths.filter(p=>{try{fs.accessSync(p,fs.constants.R_OK);return true;}catch{return false;}});
 const secretNames=['CUBE_API_KEY','ANTHROPIC_API_KEY','OPENAI_API_KEY','SANDBOXD_API_KEY'];
 return {uid:process.getuid(),gid:process.getgid(),groups:process.getgroups(),status:selected,accessibleForbiddenPaths:accessible,presentHostSecretNames:secretNames.filter(k=>!!process.env[k]),interfaces:Object.keys(os.networkInterfaces())};
}
const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function direct(target,sourcePort){
 if (++attempts>128 || Date.now()-started>18*60*1000) throw Error('guest fixture bound');
 await delay(Math.max(0,260-(Date.now()-lastStart)));lastStart=Date.now();
 return await new Promise(resolve=>{
  const start=Date.now();let finished=false;
  const s=new net.Socket();const finish=(connected,error)=>{if(finished)return;finished=true;s.destroy();resolve({target:target.label,sourcePort,connected,error,ms:Date.now()-start});};
  s.setTimeout(3000,()=>finish(false,'TIMEOUT'));
  s.once('error',e=>finish(false,e.code||'ERROR'));
  s.once('connect',()=>finish(true,null));
  s.connect({host:target.address,port:target.port,localPort:sourcePort||undefined});
 });
}
async function request(path,token,publicTarget){
 return await new Promise(resolve=>{
  const req=http.request({host:'127.0.0.1',port:3032,path:publicTarget||path,method:publicTarget?'GET':'POST',headers:publicTarget?{}:{'X-Baarcha-Bridge':token,'Content-Type':'application/json'}},res=>{
   let bytes=0,body='';res.on('data',b=>{bytes+=b.length;if(bytes>4096){req.destroy();return;}body+=b;});res.on('end',()=>resolve({status:res.statusCode,marker:body.includes(spec.marker)}));
  });req.setTimeout(3500,()=>req.destroy());req.on('error',()=>resolve({status:0,marker:false}));req.end(publicTarget?undefined:'{}');
 });
}
http.createServer({maxHeaderSize:1024},async(req,res)=>{
 const send=(code,body)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(body));};
 if(req.method!=='GET'){send(405,{});return;}
 if(req.url==='/health'){send(200,{marker:spec.marker});return;}
 if(req.url==='/identity'){send(200,identity());return;}
 if(req.url==='/stats'){send(200,{accepts,attempts});return;}
 if(req.url==='/broker'){
  if(busy){send(409,{});return;}busy=true;
  try{const good=await request('/__cube/bridge',spec.marker);const bad=await request('/__cube/bridge','wrong-fixture-token');const denied=await request('',null,'http://'+spec.targets[0].address+':'+spec.targets[0].port+'/');send(200,{good,bad,denied});}finally{busy=false;}return;
 }
 if(req.url?.startsWith('/probe?')){
  const query=new URL(req.url,'http://synthetic.invalid').searchParams;
  const peer=query.get('peer');
  if(query.get('marker')!==spec.marker || typeof peer!=='string' || net.isIP(peer)!==4 || !peer.startsWith('192.168.') || peer==='192.168.0.1' || [...query.keys()].sort().join(',')!=='marker,peer'){send(400,{});return;}
  if(busy){send(409,{});return;}busy=true;
  const targets=spec.targets.map(t=>t.label==='sibling'?{...t,address:peer}:t);
  try{const results=[];for(const t of targets){for(const port of [3000,3031,49983,0]){const r=await direct(t,port);results.push(r);if(r.connected){send(200,{peer,results,unexpectedAcceptance:true});return;}}}send(200,{peer,results});}catch{send(500,{error:'fixture bound'});}finally{busy=false;}return;
 }
 send(404,{});
}).listen(3001,'0.0.0.0');
