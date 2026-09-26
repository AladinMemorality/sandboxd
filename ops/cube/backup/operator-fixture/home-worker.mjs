// Fixed operator-owned data only. No credentials, subprocesses, models or network.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';
export const HOME_FILE='home marker\n# [1].txt';
export const LINK='relative-link', HARD='hard-link', SCRIPT='run.sh';
const sha=x=>crypto.createHash('sha256').update(x).digest('hex');
export function names(run,appRoot,ownerHome){
 assert.match(run,/^[a-f0-9]{16}$/);
 return {app:path.join(appRoot,`.operator-recovery/${run}/app marker #.txt`),home:path.join(ownerHome,`operator-recovery-${run}`)};
}
export function readRecoveryProof(run,appRoot='/home/sandbox/workspace/app',ownerHome='/home/sandbox',uid=1000){
 const n=names(run,appRoot,ownerHome),marker=`operator-recovery-${run}`;
 assert.equal(fs.realpathSync(n.home),n.home);assert.equal(fs.lstatSync(n.home).uid,uid);assert.equal(fs.lstatSync(n.home).mode&0o777,0o700);
 const file=path.join(n.home,HOME_FILE),hard=path.join(n.home,HARD),link=path.join(n.home,LINK),script=path.join(n.home,SCRIPT);
 const a=fs.lstatSync(file),b=fs.lstatSync(hard),c=fs.lstatSync(script),d=fs.lstatSync(link);
 assert(a.isFile()&&b.isFile()&&c.isFile()&&d.isSymbolicLink());
 assert.equal(a.uid,uid);assert.equal(b.uid,uid);assert.equal(c.uid,uid);assert.equal(d.uid,uid);
 assert.equal(a.mode&0o777,0o640);assert.equal(c.mode&0o777,0o750);
 assert.equal(a.dev,b.dev);assert.equal(a.ino,b.ino);assert.equal(a.nlink,2);assert.equal(b.nlink,2);
 assert.equal(fs.readlinkSync(link),HOME_FILE);assert.equal(fs.realpathSync(link),file);
 const appStat=fs.lstatSync(n.app);assert(appStat.isFile());assert.equal(appStat.uid,uid);assert.equal(appStat.mode&0o777,0o644);
 const home=fs.readFileSync(file,'utf8'),app=fs.readFileSync(n.app,'utf8'),exe=fs.readFileSync(script,'utf8');
 assert.equal(home,marker+':home');assert.equal(app,marker+':app');assert.equal(exe,'#!/bin/sh\nexit 0\n');
 return {kind:'operator-authored-recovery-data',run,app,home,home_sha256:sha(home),app_sha256:sha(app),home_file:HOME_FILE,symlink_target:HOME_FILE,hardlink_same_inode:true,hardlink_count:a.nlink,file_mode:a.mode&0o777,script_mode:c.mode&0o777,uid,ai_success:false};
}
export function createHomeFixture(run,appRoot,ownerHome,uid=1000){
 assert.equal(process.getuid(),uid);assert.equal(fs.realpathSync(ownerHome),ownerHome);assert.equal(fs.realpathSync(appRoot),appRoot);
 const n=names(run,appRoot,ownerHome);
 try{fs.mkdirSync(n.home,{mode:0o700});}
 catch(e){if(e.code!=='EEXIST')throw e;return readRecoveryProof(run,appRoot,ownerHome,uid);}
 const write=(name,bytes,mode)=>{const fd=fs.openSync(path.join(n.home,name),fs.constants.O_WRONLY|fs.constants.O_CREAT|fs.constants.O_EXCL|fs.constants.O_NOFOLLOW,mode);try{fs.writeFileSync(fd,bytes);fs.fchmodSync(fd,mode);fs.fsyncSync(fd);}finally{fs.closeSync(fd);}};
 write(HOME_FILE,`operator-recovery-${run}:home`,0o640);write(SCRIPT,'#!/bin/sh\nexit 0\n',0o750);
 fs.linkSync(path.join(n.home,HOME_FILE),path.join(n.home,HARD));fs.symlinkSync(HOME_FILE,path.join(n.home,LINK));
 for(const directory of [n.home,ownerHome]){const fd=fs.openSync(directory,'r');try{fs.fsyncSync(fd);}finally{fs.closeSync(fd);}}
 return readRecoveryProof(run,appRoot,ownerHome,uid);
}
if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 try{
  const [run]=process.argv.slice(2);assert.equal(process.argv.length,3);assert.equal(process.cwd(),'/home/sandbox/workspace/app');
  const result=createHomeFixture(run,process.cwd(),'/home/sandbox');
  const target=`.operator-recovery/${run}/home-ready.json`,bytes=JSON.stringify(result)+'\n';
  try{const fd=fs.openSync(target,fs.constants.O_WRONLY|fs.constants.O_CREAT|fs.constants.O_EXCL|fs.constants.O_NOFOLLOW,0o600);try{fs.writeFileSync(fd,bytes);fs.fsyncSync(fd);}finally{fs.closeSync(fd);}const parent=fs.openSync(path.dirname(target),'r');try{fs.fsyncSync(parent);}finally{fs.closeSync(parent);}}catch(e){if(e.code!=='EEXIST')throw e;assert.equal(fs.readFileSync(target,'utf8'),bytes);}
  // Keep one inert supervised process until the original manifest is restored.
  // No repeated writes or data regeneration after readiness.
  const timer=setInterval(()=>{},60000);for(const signal of ['SIGTERM','SIGINT'])process.once(signal,()=>{clearInterval(timer);process.exit(0);});
 }catch{console.error('Operator recovery worker refused; retained data was not repaired or overwritten.');process.exitCode=1;}
}
