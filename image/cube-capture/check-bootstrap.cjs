const assert = require('node:assert/strict');
const fs = require('node:fs');
const crypto = require('node:crypto');
const WebSocket = require('/worker/node_modules/playwright-core/lib/utilsBundle.js').ws;
const token = crypto.randomBytes(32).toString('hex');
const headers = { Authorization: 'Bearer ' + token };
const http = (port,path,options={}) => fetch('http://127.0.0.1:'+port+path, { ...options, signal:AbortSignal.timeout(5000) });
(async()=>{
 const deadline=Date.now()+40000;
 let state;
 for(;;){
  const health=await http(49983,'/health').catch(()=>null);
  if(health?.status===200){state=await health.json();break;}
  assert(Date.now()<deadline,'Chromium failed to warm');await new Promise(r=>setTimeout(r,100));
 }
 assert.equal(state.initialized,false);
 assert.equal((await http(49983,'/snapshot-ready')).status,200);
 assert.equal((await http(3031,'/health')).status,401);
 const status=fs.readFileSync('/proc/1/status','utf8');
 assert.match(status,/Uid:\s+1000\s+1000\s+1000\s+1000/);
 assert.match(status,/NoNewPrivs:\s+1/);
 assert.throws(()=>fs.readFileSync('/proc/1/environ'),{code:'EACCES'},'Go process memory/environ must be protected from worker UID');
 const initialized=await http(49983,'/init',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({envVars:{RUNTIMED_HTTP_ADDR:':3031',RUNTIMED_HTTP_TOKEN:token}})});
 assert.equal(initialized.status,204);
 assert.equal((await http(49983,'/snapshot-ready')).status,503);
 assert.equal((await http(3031,'/health',{headers})).status,200);
 assert.equal((await http(3031,'/files/content',{headers})).status,404);
 const ws=new WebSocket('ws://127.0.0.1:3031/capture/channel',{headers,perMessageDeflate:false});
 const timeout=setTimeout(()=>{ws.terminate();throw new Error('capture deadline');},15000);
 let buffer='';let captures=0;let responded=false;
 await new Promise((resolve,reject)=>{
  ws.on('error',reject);ws.on('close',()=>{if(!responded)reject(new Error('capture closed before result'));});
  ws.on('message',(bytes,binary)=>{
   assert(binary);assert(bytes.length<=65536);buffer+=bytes.toString();
   while(buffer.includes('\n')){
    const i=buffer.indexOf('\n');const message=JSON.parse(buffer.slice(0,i));buffer=buffer.slice(i+1);
    if(message.type==='ready'){captures++;ws.send(Buffer.from(JSON.stringify({type:'capture',url:'http://fixture.test/',mode:'hero',dpr:1,preview:true})+'\n'));}
    else if(message.type==='fetch'){ws.send(Buffer.from(JSON.stringify({type:'response',id:message.id,status:200,headers:{'content-type':'text/html'},body:Buffer.from('<!doctype html><html><body><h1>Pristine capture ready</h1></body></html>').toString('base64')})+'\n'));}
    else if(message.type==='result'){
     assert(message.raw.text.includes('Pristine capture ready'));assert(Buffer.from(message.screenshot,'base64').length>1000);responded=true;resolve();
    } else if(message.type==='error')reject(new Error('worker capture failed'));
   }
  });
 });
 clearTimeout(timeout);assert.equal(captures,1);ws.close();
 console.log(JSON.stringify({passed:true,checks:['UID1000','no-new-privileges','protected-Go-process','Chromium-warm-before-init','pristine-health-before-token-only','no-filesystem-API','authenticated-single-capture','binary64KiB-bridge','actual-Chromium-render']}));
})().catch(error=>{console.error(error.message);process.exitCode=1;});
