import http from 'node:http';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const [source,expected]=process.argv.slice(2);
let closed=0;
const worker=http.createServer((req,res)=>{
 if(req.url==='/api/status'){res.setHeader('content-type','application/json');res.end('{"mode":"shared-workspace"}');return;}
 res.writeHead(200,{'content-type':'video/mp4','content-length':16*1024*1024});
 const timer=setInterval(()=>res.write(Buffer.alloc(16384)),10);
 res.on('close',()=>{clearInterval(timer);closed++});
});
await new Promise(resolve=>worker.listen(0,'127.0.0.1',resolve));
const code=`import {createProxy} from ${JSON.stringify(pathToFileURL(source).href)};const s=createProxy({env:{STUDIO_WORKER_URL:process.env.FIXTURE_WORKER,STUDIO_WORKER_KEY:'owned-fixture-key'}});s.listen(0,'127.0.0.1',()=>console.log(s.address().port));`;
const child=spawn(process.execPath,['--input-type=module','-e',code],{env:{PATH:process.env.PATH,FIXTURE_WORKER:'http://127.0.0.1:'+worker.address().port},stdio:['ignore','pipe','pipe']});
let stderr='';child.stderr.on('data',d=>stderr+=d);const exit=once(child,'exit');
try{
 const [data]=await once(child.stdout,'data');const port=Number(data.toString().trim());assert(port>0);
 await new Promise((resolve,reject)=>{const req=http.get('http://127.0.0.1:'+port+'/media/owned.mp4',res=>res.once('data',()=>{res.destroy();resolve()}));req.on('error',reject)});
 await new Promise(resolve=>setTimeout(resolve,300));
 if(expected==='crash'){const [code]=await Promise.race([exit,new Promise((_,reject)=>setTimeout(()=>reject(new Error('Original did not crash')),2000))]);assert.equal(code,1);assert.match(stderr,/AbortError|unhandled.*error/is);}
 else{assert.equal(child.exitCode,null,'Proxy crashed after cancelled playback');const res=await fetch('http://127.0.0.1:'+port+'/api/status');assert.equal(res.status,200);assert.equal((await res.json()).mode,'shared-workspace');}
 assert.equal(closed,1,'Upstream stream was not closed');console.log(JSON.stringify({expected,result:'passed',upstream_closed:true,abort_error:stderr.includes('AbortError')}));
}finally{if(child.exitCode===null){child.kill();await exit;}worker.closeAllConnections();await new Promise(resolve=>worker.close(resolve));}
