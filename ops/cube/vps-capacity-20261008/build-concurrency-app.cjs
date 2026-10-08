const http=require('http'),fs=require('fs'),cp=require('child_process');
let job={state:'idle'},child,runs=0;
function start(){
 if(child||runs>=10)return false;runs++;const at=Date.now();let bytes=0;
 job={state:'running',run:runs,started:at};
 child=cp.spawn('npm',['run','build'],{cwd:process.cwd(),env:{...process.env,CI:'true'},stdio:['ignore','pipe','pipe'],detached:true});
 const proc=child;proc.stdout.on('data',b=>bytes+=b.length);proc.stderr.on('data',b=>bytes+=b.length);
 const timer=setTimeout(()=>{try{process.kill(-proc.pid,'SIGTERM')}catch{};setTimeout(()=>{try{process.kill(-proc.pid,'SIGKILL')}catch{}},2000).unref()},170000);
 proc.on('error',()=>{job={state:'failed',run:runs};child=null;clearTimeout(timer)});
 proc.on('exit',(code,signal)=>{job={state:code===0?'passed':'failed',run:runs,exit_code:code,signal,seconds:(Date.now()-at)/1000,log_bytes:bytes};child=null;clearTimeout(timer)});
 return true;
}
(async()=>{
 const {createServer}=await import('vite');const vite=await createServer({server:{middlewareMode:true,allowedHosts:true},appType:'spa'});
 http.createServer((req,res)=>{
  if(req.url==='/__capacity/status'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({...job,rss:process.memoryUsage().rss,pid:process.pid}))}
  if(req.url==='/__capacity/build'&&req.method==='POST'){res.statusCode=start()?202:409;return res.end()}
  vite.middlewares(req,res,()=>{res.statusCode=404;res.end()});
 }).listen(3000,'0.0.0.0');
 setTimeout(()=>process.exit(0),30*60*1000).unref();
})().catch(()=>process.exit(1));
