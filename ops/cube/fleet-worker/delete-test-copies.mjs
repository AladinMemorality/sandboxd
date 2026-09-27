// Root-only cleanup of exact encrypted objects recorded by this test.
import fs from 'node:fs/promises';
import {createRequire} from 'node:module';
const require=createRequire('/opt/baarcha/app/landing/package.json');
const {S3Client, HeadObjectCommand, DeleteObjectCommand}=require('@aws-sdk/client-s3');
async function main() {
  process.umask(0o077);
  if (process.getuid()!==0) throw Error('root required');
  const root='/opt/baarcha-bench/cube-fleet-20260927/capacity-100';
  const cleanup=JSON.parse(await fs.readFile(`${root}/cleanup.json`,'utf8'));
  if (!cleanup.cleanup_verified) throw Error('guest cleanup must finish first');
  const plan=JSON.parse(await fs.readFile(`${root}/plan.PRIVATE.json`,'utf8'));
  const client=new S3Client({region:'eu-central-1',maxAttempts:2});
  let deleted=0;
  let operation='receipt', sourceIndex=-1;
  const failures=[];
  try {
    for (const source of plan.sources) {
      sourceIndex++;
      operation='receipt';
      const receipt=JSON.parse(await fs.readFile(`${root}/sources/${source.sandbox_id}/s3-receipt.PRIVATE.json`,'utf8'));
      if (receipt.sandbox_id!==source.sandbox_id || receipt.bucket!=='punicas' || !/^baarcha\/cube\/test-copies\/[a-f0-9]{64}\/[a-f0-9-]+\.zip\.enc$/.test(receipt.object_key)) throw Error('object scope differs');
      const target={Bucket:receipt.bucket,Key:receipt.object_key};
      try {
      let head;
      operation='head-before-delete';
      try {head=await client.send(new HeadObjectCommand(target));}
      catch(e) {if (e.$metadata?.httpStatusCode===404) {deleted++;continue;} throw e;}
      operation=head.VersionId?'delete-version':'delete-object';
      await client.send(new DeleteObjectCommand({...target,...(head.VersionId?{VersionId:head.VersionId}:{})}));
      operation='head-after-delete';
      try {await client.send(new HeadObjectCommand(target)); throw Error('object still exists');}
      catch(e) {if (e.$metadata?.httpStatusCode!==404) throw e;}
      deleted++;
      } catch(e) {
        failures.push({source_index:sourceIndex,operation,error:e.name,status:e.$metadata?.httpStatusCode??null});
      }
    }
    const result={deleted,verified:deleted===plan.sources.length,failures};
    await fs.writeFile(`${root}/s3-cleanup.json`,JSON.stringify(result),{mode:0o600});
    console.log(JSON.stringify(result));
    if (!result.verified) process.exitCode=1;
  } finally {client.destroy();}
}
main().catch(e=>{console.error('Test object cleanup failed:',e.name);process.exitCode=1;});
