const fs=require('node:fs');
// Diagnostic aggregate only. No tool content, credential or environment output.
if(process.getuid()===0){process.setgroups([]);process.setgid(1000);process.setuid(1000);}
if(process.getuid()!==1000)process.exit(241);
const p='/home/sandbox/.runtimed/tasks/01M3D3Z2T9CM6WSW9QB311KXB7/stream.jsonl';
if(fs.realpathSync(p)!==p)process.exit(242);
const fd=fs.openSync(p,fs.constants.O_RDONLY|fs.constants.O_NOFOLLOW),st=fs.fstatSync(fd);
if(!st.isFile()||st.size>16*1024*1024)process.exit(243);
const data=fs.readFileSync(fd,'utf8');fs.closeSync(fd);
const rows=data.split('\n').filter(Boolean).map(x=>JSON.parse(x));
const uses=new Map(),results=[];
for(const row of rows){const e=row.ev;if(!e)continue;const content=e.message?.content;if(!Array.isArray(content))continue;
 for(const b of content){if(e.type==='assistant'&&b.type==='tool_use')uses.set(b.id,{name:b.name,input:b.input,time:Date.parse(row.ts)});if(e.type==='user'&&b.type==='tool_result')results.push({b,time:Date.parse(row.ts)});}}
const text=b=>typeof b.content==='string'?b.content:Array.isArray(b.content)?b.content.filter(x=>x.type==='text').map(x=>x.text||'').join(''):'';
const matched=results.filter(x=>uses.has(x.b.tool_use_id));
const source=matched.filter(x=>{const u=uses.get(x.b.tool_use_id);return u.name==='Read'&&u.input?.file_path==='/home/sandbox/workspace/app/server.mjs';});
const query=process.argv[1];let answer;
if(query==='results')answer=Math.min(31,matched.length)+32*Math.min(6,matched.filter(x=>x.b.is_error).length);
else if(query==='source_flags')answer=(source.length?1:0)|(source.some(x=>text(x.b).length>1000)?2:0)|(source.some(x=>x.b.is_error)?4:0)|(source.some(x=>/express|createServer/.test(text(x.b)))?8:0)|(source.some(x=>text(x.b).length===0)?16:0)|(matched.some(x=>x.time-uses.get(x.b.tool_use_id).time>1000)?32:0)|(matched.length===uses.size?64:0);
else if(query==='latency_flags')answer=(matched.length===uses.size?1:0)|(matched.some(x=>x.time-uses.get(x.b.tool_use_id).time>1000)?2:0)|(matched.some(x=>x.time-uses.get(x.b.tool_use_id).time>5000)?4:0)|(matched.some(x=>x.b.is_error)?8:0);
else process.exit(244);
process.exit(10+answer);
