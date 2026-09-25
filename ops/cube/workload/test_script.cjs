'use strict';
// Exact embedded script, deterministic timers/page-map. No real large allocation,
// CPU spin, listener, guest or outbound call.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const script = fs.readFileSync(path.join(__dirname,'../../../control-plane/internal/store/testdata/cube-workload.cjs'),'utf8');
function fixture() {
  let allocations=0,touched=0,handler,interval,clock=0,killed=false,closed=false,gcs=0;
  const timeouts=[],immediates=[],pages=new Map();
  vm.runInNewContext(script, {
    global:{gc(){gcs++;}},
    require(name) {
      if(name==='node:http')return {createServer(fn){handler=fn;return {listen(port,host){assert.equal(port,3000);assert.equal(host,'0.0.0.0');},close(){closed=true;}};}};
      if(name==='node:crypto')return {randomBytes(){return {toString(){return 'a'.repeat(32);}};}};
      if(name==='node:perf_hooks')return {performance:{now(){return clock++;}}};
      throw new Error('unexpected module/outbound dependency: '+name);
    },
    Buffer:{allocUnsafeSlow(bytes){assert.equal(bytes,512*1024*1024);allocations++;return new Proxy({length:bytes},{set(target,key,value){assert.equal(Number(key)%4096,0);assert.equal(value,90);touched++;pages.set(key,value);return true;},get(target,key){return key==='length'?bytes:pages.get(key);}});}},
    process:{pid:42,memoryUsage(){return {rss:513*1024*1024};},cpuUsage(){return {user:clock,system:1};},exit(code){assert.equal(code,0);killed=true;}},
    setInterval(fn,ms){assert.equal(ms,100);interval=fn;return 1;},clearInterval(id){assert.ok(id===undefined||id===1);interval=null;},
    setImmediate(fn){immediates.push(fn);},
    setTimeout(fn,ms){timeouts.push({fn,ms});return {unref(){}};},
  });
  function request(method,url){let code=200,body;handler({method,url},{setHeader(){},writeHead(n){code=n;},end(raw){body=raw?JSON.parse(raw):null;}});return {code,body};}
  return {request,timeouts,immediates,cpu(){interval();},state(){return {allocations,touched,clock,killed,closed,gcs,interval};}};
}
const f=fixture();
assert.equal(f.request('GET','/health').body.bytes,0);
assert.equal(f.request('POST','/bad').code,409);
const start=f.request('POST','/start').body;
assert.equal(start.bytes,512*1024*1024);assert.equal(start.active,false);assert.equal(start.pages,0);
assert.equal(f.state().touched,0); // response precedes ANY page touching
assert.equal(f.request('POST','/start').code,409);assert.equal(f.state().allocations,1);
for(let chunk=0;chunk<128;chunk++){
  assert.equal(f.immediates.length,1);f.immediates.shift()();
  assert.equal(f.state().touched,(chunk+1)*1024); // exactly4MiB per turn
}
assert.equal(f.immediates.length,0);assert.equal(f.request('GET','/health').body.pages,131072*90);
f.cpu();assert.equal(f.request('GET','/health').body.cycles,1);assert.ok(f.state().clock<=10);
assert.deepEqual(f.timeouts.map(t=>t.ms),[8*60000,80000,60000]);
f.timeouts.find(t=>t.ms===60000).fn();assert.equal(f.request('GET','/health').body.bytes,0);assert.equal(f.state().interval,null);
assert.equal(f.request('POST','/start').code,409);
f.timeouts.find(t=>t.ms===8*60000).fn();assert.ok(f.state().killed&&f.state().closed);
// A stalled preparation cannot extend80s or later restart memory/CPU.
const stalled=fixture();stalled.request('POST','/start');stalled.immediates.shift()();
assert.equal(stalled.state().touched,1024);
stalled.timeouts.find(t=>t.ms===80000).fn();stalled.immediates.shift()();
assert.equal(stalled.state().touched,1024);assert.equal(stalled.state().interval,null);
assert.equal(stalled.request('GET','/health').body.bytes,0);assert.equal(stalled.request('POST','/start').code,409);
assert.equal(stalled.state().allocations,1);assert.ok(stalled.state().gcs>0);
console.log('Fixed allocation, asynchronous4MiB chunks, immediate acknowledgment, CPU duty,60s stop, absolute80s cap and no reallocation passed.');
