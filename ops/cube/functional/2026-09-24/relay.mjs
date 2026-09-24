import assert from 'node:assert/strict';
import http from 'node:http';
import fs from 'node:fs';
import crypto from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {forwardBody} from './relay-stream.mjs';
const stage=process.cwd(),prod='/opt/baarcha/app/landing';
const lib=async p=>import(pathToFileURL(`${prod}/src/lib/${p}`).href);
const {sql,closePg}=await lib('pg.ts'),{writeEntry}=await lib('platform/credit.ts'),{bridgeEnvFor,bridgeOwner}=await lib('bridge/bridge.ts');
const {default:postgres}=await import(pathToFileURL(`${prod}/node_modules/postgres/src/index.js`).href);
const {bridgeFileResponse}=await import(pathToFileURL(`${stage}/file.ts`).href);
const settings=JSON.parse(fs.readFileSync('settings.json','utf8'));
const schema='cube_smoke_'+crypto.randomBytes(6).toString('hex');
const isolated=postgres(settings.testDatabaseURL,{max:2});
const db=sql();let owner,server,closed=false,modelCalls=0,counts=0,libraryCalls=0,fileCalls=0;
const project='01M3'+Array.from(crypto.randomBytes(22),b=>'0123456789ABCDEFGHJKMNPQRSTVWXYZ'[b%32]).join('');
const id=crypto.randomBytes(12).toString('base64url'),control=crypto.randomBytes(24).toString('hex');
const bytes=Buffer.from('cube-functional-private-file\n');
let token;
async function finish(reason){
 if(closed)return;closed=true;
 let usage=[];let accounting_error=false;
 try {if(owner)usage=await db`SELECT u.endpoint,u.model,u.outcome,u.input_tokens,u.output_tokens FROM api_usage u JOIN api_key k ON k.id=u.key_id WHERE k.waitlist_id=${owner}`;}catch{accounting_error=true;}
 const metered=usage.some(u=>u.endpoint==='agent.messages'&&u.outcome==='ok'&&u.input_tokens>0);
 const report={reason,modelCalls,counts,libraryCalls,fileCalls,metered,accounting_error,usage,project,temporary_owner_removed:false,isolated_schema_removed:false};
 try{if(owner){await db`DELETE FROM waitlist WHERE id=${owner}`;report.temporary_owner_removed=true;}await isolated.unsafe(`DROP SCHEMA IF EXISTS ${schema} CASCADE`);report.isolated_schema_removed=true;}
 finally{fs.writeFileSync('relay-report.json',JSON.stringify(report,null,2),{mode:0o600});fs.rmSync('fixture.json',{force:true});await isolated.end();await closePg();server?.close();}
 console.log(JSON.stringify({finished:true,reason,modelCalls,metered,libraryCalls,fileCalls,cleanup:report.temporary_owner_removed&&report.isolated_schema_removed}));
 setTimeout(()=>process.exit(metered?0:1),100);
}
try{
 assert(process.env.AGENT_GATEWAY_SECRET,'host gateway unavailable');
 const unique=crypto.randomUUID();
 [{id:owner}]=await db`INSERT INTO waitlist(google_sub,email,name) VALUES(${`cube-functional-${unique}`},${`cube-functional-${unique}@example.invalid`},'Temporary Cube functional verification') RETURNING id`;
 await writeEntry(owner,{ref:`cube-functional-${unique}`,kind:'grant',millimes:1000,note:'Temporary bounded Cube functional verification; cleanup after attribution'});
 token=(await bridgeEnvFor(owner,project)).BRIDGE_TOKEN;
 await isolated.unsafe(`CREATE SCHEMA ${schema}`);
 await isolated.unsafe(`CREATE TABLE ${schema}.upload (id text primary key, waitlist_id integer, project_id text, name text, mime text, bytes integer, sha256 text, deleted_at text)`);
 await isolated.unsafe(`INSERT INTO ${schema}.upload VALUES ($1,$2,$3,'fixture.txt','text/plain',$4,$5,NULL)`,[id,owner,project,bytes.length,crypto.createHash('sha256').update(bytes).digest('hex')]);
 server=http.createServer(async(req,res)=>{
  try{
   const input=[];let size=0;for await(const chunk of req){size+=chunk.length;if(size>256*1024){res.writeHead(413).end();return;}input.push(chunk);}
   const body=JSON.parse(Buffer.concat(input).toString()||'{}');const uri=new URL(req.url,'http://localhost');if(uri.search && uri.search!=='?beta=true'){res.writeHead(400).end();return;}
   if(uri.pathname==='/finish'&&body.control===control){res.end('{}');void finish('requested');return;}
   if(uri.pathname==='/health'){res.end('{"ready":true}');return;}
   const count=uri.pathname==='/claude-code/anthropic/v1/messages/count_tokens';
   if(count||uri.pathname==='/claude-code/anthropic/v1/messages'){
    if(req.method!=='POST'||req.headers['x-baarcha-bridge']!==token){res.writeHead(403).end();return;}
    if(count?++counts>8:++modelCalls>4){res.writeHead(429).end('{"error":"smoke budget exhausted"}');return;}
    if(!count){body.max_tokens=Math.min(Number(body.max_tokens)||1024,2048);if(body.thinking)delete body.thinking;}
    const abort=new AbortController();res.on('close',()=>abort.abort());
    const upstream=await fetch('http://127.0.0.1:3100/api/v1/messages'+(count?'/count_tokens':''),{method:'POST',headers:{'content-type':'application/json','x-api-key':process.env.AGENT_GATEWAY_SECRET,'x-baarcha-bridge':token,'anthropic-version':'2023-06-01',...(req.headers['anthropic-beta']?{'anthropic-beta':req.headers['anthropic-beta']}:{})},body:JSON.stringify(body),signal:AbortSignal.any([abort.signal,AbortSignal.timeout(120000)])});
    res.writeHead(upstream.status,{'content-type':upstream.headers.get('content-type')||'application/json'});
    forwardBody(upstream.body,res);return;
   }
   if(uri.pathname==='/api/bridge'&&req.method==='POST'){
    const auth=req.headers.authorization;
    const scope=typeof auth==='string'&&auth.startsWith('Bearer ')?await bridgeOwner(auth.slice(7)):null;
    if(!scope||scope.projectId!==project||scope.waitlistId!==owner){res.writeHead(401).end();return;}
    let response;
    if(body.kind==='library'){libraryCalls++;response=Response.json({files:[{id,name:'fixture.txt',bytes:bytes.length,mime:'text/plain',requires_bridge_authorization:true}],file_download:'POST same BRIDGE_URL {kind:"file",id,offset:0}; decode data_b64.'});}
    else if(body.kind==='file'){
     fileCalls++;const abort=new AbortController();res.on('close',()=>abort.abort());
     response=await bridgeFileResponse(body,scope,abort.signal,{getUpload:async key=>(await isolated.unsafe(`SELECT * FROM ${schema}.upload WHERE id=$1`,[key]))[0]??null,openUploadStream:async(_row,range)=>new ReadableStream({start(c){c.enqueue(bytes.subarray(range.start,range.end+1));c.close();}})});
    }else response=Response.json({error:'Only library/file are in this fixture.'},{status:400});
    res.writeHead(response.status,Object.fromEntries(response.headers));res.end(Buffer.from(await response.arrayBuffer()));return;
   }
   res.writeHead(404).end();
  }catch{if(!res.headersSent)res.writeHead(502);res.end('{"error":"functional relay failed"}');}
 });
 await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(18291,'127.0.0.1',resolve);});
 fs.writeFileSync('fixture.json',JSON.stringify({project,owner,bridgeToken:token,fileId:id,control}),{mode:0o600});
 console.log('READY isolated functional relay; no credentials logged');
 setTimeout(()=>void finish('deadline'),10*60*1000);
 process.on('SIGTERM',()=>void finish('terminated'));process.on('SIGINT',()=>void finish('interrupted'));
}catch{await finish('setup-failed');}
