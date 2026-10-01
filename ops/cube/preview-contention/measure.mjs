// Read-only operator measurement; run on the VPS with landing.env loaded.
// Use only an authorized sandbox. Tokens and query strings are never logged.
import {performance} from 'node:perf_hooks';
import {writeFile} from 'node:fs/promises';
import {chromium} from '/opt/baarcha/app/landing/node_modules/playwright-core/index.mjs';
import {getSandbox,sandboxPreviewAccess,listFiles,readSandboxFile} from '/opt/baarcha/app/landing/src/lib/sandboxd/client.ts';
import {detectPages} from '/opt/baarcha/app/landing/src/lib/projects/pages.ts';
const id=process.argv[2];if(!id||!/^[A-Za-z0-9-]+$/.test(id))throw new Error('Usage: measure.mjs <authorized-sandbox-id>');const report={timings:[],requests:[]};
async function time(name,fn){const start=performance.now();try{const v=await fn();report.timings.push({name,ms:Math.round(performance.now()-start)});console.log(JSON.stringify(report.timings.at(-1)));return v;}catch{console.log(JSON.stringify({name,failed:true,ms:Math.round(performance.now()-start)}));throw new Error(name+' failed');}}
let browser;
try {
 const sb=await time('get-sandbox',()=>getSandbox(id));console.log(JSON.stringify({status:sb.status}));
 const access=await time('preview-access',()=>sandboxPreviewAccess(sb));
 const files=await time('list-files',()=>listFiles(id));
 await time('read-one-file',()=>readSandboxFile(id,'src/App.tsx'));
 browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 const page=await browser.newPage();
 page.on('requestfinished',request=>{const t=request.timing();let u;try{u=new URL(request.url());}catch{return;}if(u.pathname.includes('preview-auth'))return;report.requests.push({path:u.pathname,type:request.resourceType(),start:t.startTime,dns:t.domainLookupEnd-t.domainLookupStart,connect:t.connectEnd-t.connectStart,ttfb:t.responseStart-t.requestStart,total:t.responseEnd});});
 page.on('requestfailed',request=>{let u;try{u=new URL(request.url());}catch{return;}report.requests.push({path:u.pathname,failed:true});});
 const target=new URL('/__sandboxd/preview-auth',access.url);target.searchParams.set('token',access.token);target.searchParams.set('path','/');
 await time('browser-cold-load',()=>page.goto(target.href,{waitUntil:'load',timeout:120000}));
 await page.waitForTimeout(1500);
 report.coldRequests=report.requests;report.requests=[];
 await time('browser-warm-with-page-discovery',()=>Promise.all([page.reload({waitUntil:'load',timeout:120000}),detectPages(files.map(f=>f.path),p=>readSandboxFile(id,p))]));
 await page.waitForTimeout(1500);
 console.log(JSON.stringify({coldSlowest:report.coldRequests.sort((a,b)=>(b.total||0)-(a.total||0)).slice(0,8),warmSlowest:report.requests.sort((a,b)=>(b.total||0)-(a.total||0)).slice(0,8)}));
 await writeFile('/opt/baarcha-bench/preview-contention-measurement.json',JSON.stringify(report,null,2),{mode:0o600});
}finally{await browser?.close();}
