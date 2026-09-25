#!/usr/bin/env node
// Operator-invoked only. No scheduling, IAM changes, object deletion or restore.
import fs from 'node:fs/promises';
import { constants } from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';

export const RECIPIENT = '25017F865BE80AA7E7E8C8925C595C2637931730';
const PART_BYTES = 128 * 1024 * 1024;
const MAX_BYTES = 1024 ** 4;
const need = (v, message) => { if (!v) throw Error(message); };
const hex = /^[a-f0-9]{64}$/;
async function privatePath(value, directory = false) {
  need(path.isAbsolute(value) && path.resolve(value) === value && await fs.realpath(value) === value, 'canonical private path required');
  const s = await fs.stat(value);
  need(s.uid === process.getuid() && (s.mode & 0o777) === (directory ? 0o700 : 0o600) && (directory ? s.isDirectory() : s.isFile()), 'private ownership/mode required');
  return s;
}
async function jsonFile(value) {
  const s = await privatePath(value); need(s.size <= 1024 * 1024, 'oversized private JSON');
  return JSON.parse(await fs.readFile(value, 'utf8'));
}
async function writeNew(file, value) {
  const f = await fs.open(file, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
  try { await f.writeFile(JSON.stringify(value, null, 2) + '\n'); await f.sync(); } finally { await f.close(); }
}
async function syncDir(dir) { const f = await fs.open(dir, 'r'); try { await f.sync(); } finally { await f.close(); } }
async function fileHash(f, size) {
  const h = crypto.createHash('sha256'); const buffer = Buffer.alloc(1024 * 1024);
  for (let offset = 0; offset < size;) {
    const { bytesRead } = await f.read(buffer, 0, Math.min(buffer.length, size - offset), offset);
    need(bytesRead > 0, 'short ciphertext'); h.update(buffer.subarray(0, bytesRead)); offset += bytesRead;
  }
  return h.digest('hex');
}
const same = (a, b) => ['dev', 'ino', 'size', 'mtimeNs', 'ctimeNs'].every(k => a[k] === b[k]);

// send(operationName,input) is injectable for offline tests. Production uses the
// installed official S3 SDK, maxAttempts=1, its normal AWS origin and credentials.
export async function transfer(config, send, { partBytes = PART_BYTES } = {}) {
  need(config.version === 1 && typeof config.readback_only === 'boolean', 'explicit version/readback mode required');
  need(Object.keys(config).sort().join(',') === 'bucket,manifest,output,prefix,readback_only,version', 'unexpected transfer configuration');
  need(/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(config.bucket), 'reviewed bucket required');
  need(/^[A-Za-z0-9_-]+(?:\/[A-Za-z0-9_-]+)*$/.test(config.prefix), 'bounded backup prefix required');
  need(Number.isSafeInteger(partBytes) && partBytes >= 5 * 1024 * 1024 && partBytes <= PART_BYTES, 'bounded multipart size required');
  const manifestStat = await privatePath(config.manifest);
  const manifest = await jsonFile(config.manifest);
  need(manifest.version === 1 && manifest.kind === 'encrypted-cold-cube-pair' && manifest.recipient_fingerprint === RECIPIENT, 'reviewed sealed backup required');
  need(hex.test(manifest.ciphertext_sha256) && hex.test(manifest.capture_manifest_sha256), 'trusted backup digests required');
  need(Number.isFinite(manifest.captured_at) && manifest.captured_at > 0 && manifest.captured_at <= manifest.sealed_at && manifest.sealed_at <= Date.now() / 1000, 'immutable capture time required');
  const manifestHash = crypto.createHash('sha256').update(await fs.readFile(config.manifest)).digest('hex');
  const size = manifest.file_identity?.bytes;
  need(Number.isSafeInteger(size) && size > 0 && size <= MAX_BYTES && Math.ceil(size / partBytes) <= 10000, 'bounded ciphertext required');
  const sourceStat = await privatePath(manifest.ciphertext_path);
  need(sourceStat.size === size, 'ciphertext size changed');
  await privatePath(path.dirname(config.output), true);
  need(path.resolve(config.output) === config.output, 'canonical new output required');
  await fs.mkdir(config.output, { mode: 0o700 }); // existing job is never reused
  const space = await fs.statfs(config.output);
  need(space.bavail * space.bsize >= size + 64 * 1024 * 1024, 'insufficient readback space');
  const key = `${config.prefix}/cube/full/${Math.floor(manifest.captured_at)}-${manifest.ciphertext_sha256}.tar.gpg`;
  let sequence = 0; let uploadId; let completed = false;
  const journal = async value => { await writeNew(path.join(config.output, `phase-${String(sequence++).padStart(3, '0')}.json`), value); await syncDir(config.output); };
  const source = await fs.open(manifest.ciphertext_path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const before = await source.stat({ bigint: true });
    need(await fileHash(source, size) === manifest.ciphertext_sha256 && same(before, await source.stat({ bigint: true })), 'ciphertext integrity changed');
    await journal({ phase: 'verified-source', bucket: config.bucket, key, bytes: size, sha256: manifest.ciphertext_sha256, manifest_sha256: manifestHash, readback_only: config.readback_only });
    if (!config.readback_only) {
      const created = await send('CreateMultipartUpload', { Bucket: config.bucket, Key: key, ContentType: 'application/pgp-encrypted', Metadata: { sha256: manifest.ciphertext_sha256, 'capture-manifest-sha256': manifest.capture_manifest_sha256 } });
      uploadId = created.UploadId; need(typeof uploadId === 'string' && uploadId.length > 0 && uploadId.length <= 4096, 'missing multipart identity');
      await journal({ phase: 'multipart-created', upload_id: uploadId, key });
      const parts = []; const h = crypto.createHash('sha256');
      for (let offset = 0, number = 1; offset < size; number++) {
        const body = Buffer.alloc(Math.min(partBytes, size - offset));
        let read = 0;
        while (read < body.length) { const x = await source.read(body, read, body.length - read, offset + read); need(x.bytesRead > 0, 'short upload source'); read += x.bytesRead; }
        h.update(body);
        const part = await send('UploadPart', { Bucket: config.bucket, Key: key, UploadId: uploadId, PartNumber: number, Body: body, ContentLength: body.length, ContentMD5: crypto.createHash('md5').update(body).digest('base64') });
        need(typeof part.ETag === 'string' && part.ETag.length > 0, 'missing part acknowledgement');
        parts.push({ PartNumber: number, ETag: part.ETag }); offset += body.length;
        await journal({ phase: 'part-acknowledged', part: number, uploaded_bytes: offset });
      }
      need(h.digest('hex') === manifest.ciphertext_sha256 && same(before, await source.stat({ bigint: true })), 'source changed during upload');
      await journal({ phase: 'completing', upload_id: uploadId });
      await send('CompleteMultipartUpload', { Bucket: config.bucket, Key: key, UploadId: uploadId, MultipartUpload: { Parts: parts }, IfNoneMatch: '*' });
      completed = true; await journal({ phase: 'completed' });
    }
    const obj = await send('GetObject', { Bucket: config.bucket, Key: key });
    need(obj.ContentLength === size && obj.Body?.[Symbol.asyncIterator], 'readback shape differs');
    const pending = path.join(config.output, 'readback.UNVERIFIED.gpg');
    const target = await fs.open(pending, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
    const h = crypto.createHash('sha256'); let bytes = 0;
    try {
      for await (const chunk of obj.Body) { bytes += chunk.length; need(bytes <= size, 'oversized readback'); h.update(chunk); await target.writeFile(chunk); }
      need(bytes === size && h.digest('hex') === manifest.ciphertext_sha256, 'readback integrity mismatch');
      await target.sync();
    } finally { await target.close(); obj.Body.destroy?.(); }
    need(same(before, await source.stat({ bigint: true })) && (await fs.stat(config.manifest)).size === manifestStat.size && crypto.createHash('sha256').update(await fs.readFile(config.manifest)).digest('hex') === manifestHash, 'sealed generation changed');
    await fs.link(pending, path.join(config.output, 'readback.gpg')); await syncDir(config.output);
    const evidence = { version: 1, kind: 'offhost-full-readback', destination_id: `s3://${config.bucket}/${key}`, bytes, ciphertext_sha256: manifest.ciphertext_sha256, manifest_sha256: manifestHash, captured_at: manifest.captured_at, verified_at: Date.now() / 1000, full_object_get_hash_verified: true, application_restore_verified: false };
    const evidencePath = path.join(config.output, 'copy-evidence.json');
    await writeNew(evidencePath, evidence);
    await writeNew(path.join(config.output, 'offhost-receipt.json'), { version: 1, kind: 'offhost-copy', manifest_sha256: manifestHash, ciphertext_sha256: manifest.ciphertext_sha256, evidence_sha256: crypto.createHash('sha256').update(await fs.readFile(evidencePath)).digest('hex'), verified_at: Date.now() / 1000, verified_bytes: size, offhost: true, destination_id: evidence.destination_id });
    await journal({ phase: 'readback-verified' });
    return evidence;
  } catch (error) {
    // Never delete the completed object. An uncertain completion is reconciled by
    // a separate fresh readback-only job with the same trusted seal manifest.
    await journal({ phase: 'failed', completed_acknowledged: completed, upload_id: uploadId ?? null, automatic_abort_attempted: false });
    throw error;
  } finally { await source.close(); }
}

async function main() {
  need(process.platform === 'linux' && process.getuid() === 0 && process.argv.length === 3, 'native root and one private config path required');
  process.umask(0o077);
  const config = await jsonFile(process.argv[2]);
  const require = createRequire('/opt/baarcha/app/landing/package.json');
  const sdk = require('@aws-sdk/client-s3');
  const region = process.env.PUNICAS_REGION ?? 'eu-central-1';
  need(/^[a-z]{2}-[a-z]+-\d$/.test(region), 'reviewed standard AWS region required');
  // Pin AWS origin rather than accepting AWS_ENDPOINT_URL pointing to a local
  // emulator, which would not prove an independent off-host copy.
  const client = new sdk.S3Client({ region, endpoint: `https://s3.${region}.amazonaws.com`, maxAttempts: 1 });
  try { const out = await transfer(config, (name, input) => client.send(new sdk[name + 'Command'](input))); console.log(JSON.stringify({ readback_verified: true, bytes: out.bytes, ciphertext_sha256: out.ciphertext_sha256, application_restore_verified: false })); }
  finally { client.destroy(); }
}
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) main().catch(() => { console.error('Off-host backup transfer failed; retain private journal and reconcile exact object/upload ID.'); process.exitCode = 1; });
