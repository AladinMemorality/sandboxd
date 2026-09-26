// Read-only repeatable snapshot, never a tenant mutation or model operation.
import fs from 'node:fs/promises';
import {createRequire} from 'node:module';
import {spawn} from 'node:child_process';
import crypto from 'node:crypto';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import assert from 'node:assert/strict';
const sha=b=>crypto.createHash('sha256').update(b).digest('hex');
const IMAGE='postgres@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24';
async function write(p,b){const f=await fs.open(p,'wx',0o600);try{await f.writeFile(b);await f.sync();}finally{await f.close();}}
async function main(){
 assert(process.platform==='linux'&&process.getuid()===0&&process.argv.length===3);process.umask(0o077);
 const stage=process.argv[2],parent=path.dirname(stage);assert(stage.startsWith('/opt/baarcha-bench/cube-platform-pg-')&&path.resolve(stage)===stage);assert.equal(await fs.realpath(parent),parent);await fs.mkdir(stage,{mode:0o700});
 const require=createRequire('/opt/baarcha/app/landing/package.json'),sql=require('postgres')(process.env.DATABASE_URL,{max:1,connect_timeout:5,connection:{statement_timeout:150000}});
 const source=await fs.readFile(path.join(path.dirname(fileURLToPath(import.meta.url)),'provenance.sql'),'utf8');
 const u=new URL(process.env.DATABASE_URL);assert(['postgres:','postgresql:'].includes(u.protocol)&&!u.search&&!u.hash);
 const env={PATH:'/usr/bin:/bin',PGHOST:u.hostname,PGPORT:u.port||'5432',PGDATABASE:decodeURIComponent(u.pathname.slice(1)),PGUSER:decodeURIComponent(u.username),PGPASSWORD:decodeURIComponent(u.password),PGCONNECT_TIMEOUT:'10'};
 await write(path.join(stage,'intent.json'),JSON.stringify({version:1,read_only:true,created_at:new Date().toISOString(),provenance_sql_sha256:sha(source),complete:false})+'\n');
 try{
  await sql.begin('isolation level repeatable read read only',async tx=>{
   await tx.unsafe("SET LOCAL TIME ZONE 'UTC'");
   const [meta]=await tx`SELECT current_setting('server_version_num')::integer AS version,pg_database_size(current_database()) AS bytes,pg_export_snapshot() AS snapshot`;
   assert(meta.version>=170000&&meta.version<180000&&Number(meta.bytes)<=256*1024*1024&&/^[0-9A-F-]+$/.test(meta.snapshot));
   const [row]=await tx.unsafe(source);assert(row.proof&&row.proof.fixture_owners===2&&row.proof.canary?.owner===103&&row.proof.canary?.visibility==='private');
   const dump=await fs.open(path.join(stage,'platform-db.INCOMPLETE'),'wx',0o600),log=await fs.open(path.join(stage,'dump.PRIVATE.log'),'wx',0o600);
   try{
    await new Promise((resolve,reject)=>{const child=spawn('/usr/bin/prlimit',['--fsize=134217728:134217728','--','/usr/bin/pg_dump','--format=custom','--no-owner','--no-privileges','--snapshot='+meta.snapshot],{env,stdio:['ignore',dump.fd,log.fd],timeout:150000});child.once('error',reject);child.once('exit',(code,signal)=>code===0&&!signal?resolve():reject(Error('Dump refused')));});await dump.sync();await log.sync();
   }finally{await dump.close();await log.close();}
   const dumpFile=path.join(stage,'platform-db.INCOMPLETE'),info=await fs.stat(dumpFile);assert(info.size>0&&info.size<=128*1024*1024);const digest=sha(await fs.readFile(dumpFile));
   await fs.rename(dumpFile,path.join(stage,'platform-db'));
   await write(path.join(stage,'source.json'),JSON.stringify({version:1,kind:'independent-platform-pg-snapshot',captured_at:new Date().toISOString(),source_major:17,source_bytes:Number(meta.bytes),image:IMAGE,provenance_sql_sha256:sha(source),dump:{bytes:info.size,sha256:digest},proof:row.proof,full_pair:false,http_acl_verified:false},null,2)+'\n');
  });
  const d=await fs.open(stage,'r');try{await d.sync();}finally{await d.close();}console.log(JSON.stringify({read_only_snapshot_captured:true,restore_verified:false,full_pair:false}));
 }finally{await sql.end({timeout:5});}
}
main().catch(()=>{console.error('Read-only PostgreSQL snapshot failed; retain private incomplete evidence, do not reuse as a completed dump.');process.exitCode=1;});
