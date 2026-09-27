#!/usr/bin/env node
// Immutable private project source objects. No guest credentials, live data,
// deployment pointer updates, ACL changes, bucket creation, or object deletion.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';

const MAX_BYTES = 256 * 1024 * 1024;
const identifier = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const sha = data => crypto.createHash('sha256').update(data).digest('hex');
const need = (condition, message) => { if (!condition) throw Error(message); };
function target(config) {
  need(config.version === 1 && identifier.test(config.project_id) && identifier.test(config.revision_id), 'invalid source identity');
  need(/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(config.bucket), 'invalid bucket');
  need(/^[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)*$/.test(config.prefix), 'invalid private prefix');
  return `${config.prefix}/projects/${config.project_id}/revisions/${config.revision_id}`;
}
async function get(send, bucket, key, limit) {
  const obj = await send('GetObject', { Bucket: bucket, Key: key });
  need(Number.isSafeInteger(obj.ContentLength) && obj.ContentLength > 0 && obj.ContentLength <= limit, 'invalid object length');
  need(obj.Body?.[Symbol.asyncIterator], 'missing object stream');
  const chunks = []; let size = 0;
  try {
    for await (const chunk of obj.Body) { size += chunk.length; need(size <= limit && size <= obj.ContentLength, 'oversized object'); chunks.push(Buffer.from(chunk)); }
    need(size === obj.ContentLength, 'truncated object');
    return Buffer.concat(chunks);
  } finally { obj.Body.destroy?.(); }
}
async function putImmutable(send, bucket, key, body, contentType) {
  try {
    await send('PutObject', {
      Bucket: bucket, Key: key, Body: body, ContentType: contentType,
      IfNoneMatch: '*', ServerSideEncryption: 'AES256',
      ChecksumSHA256: crypto.createHash('sha256').update(body).digest('base64'),
      Metadata: { sha256: sha(body) },
    });
  } catch (error) {
    // A retry after uncertain completion can only acknowledge identical bytes.
    if (error?.$metadata?.httpStatusCode !== 412) throw error;
  }
  const copy = await get(send, bucket, key, body.length);
  need(copy.equals(body), 'immutable object conflict or readback mismatch');
}
function verify(source, manifest) {
  need(manifest.version === 1 && manifest.kind === 'private-project-source', 'wrong source format');
  need(source.length > 0 && source.length <= MAX_BYTES && manifest.bytes === source.length && manifest.source_sha256 === sha(source), 'source checksum mismatch');
  need(Array.isArray(manifest.files) && manifest.files.length > 0 && Array.isArray(manifest.excluded), 'missing source inventory');
  need(/^[a-f0-9]{64}$/.test(manifest.dependency_key), 'missing dependency cache identity');
}
export async function publish(config, source, manifest, send) {
  const key = target(config); verify(source, manifest);
  const access = await send('GetPublicAccessBlock', { Bucket: config.bucket });
  need(['BlockPublicAcls', 'IgnorePublicAcls', 'BlockPublicPolicy', 'RestrictPublicBuckets']
    .every(name => access.PublicAccessBlockConfiguration?.[name] === true), 'private source bucket must block all public access');
  const manifestBody = Buffer.from(JSON.stringify(manifest));
  need(manifestBody.length <= 1024 * 1024, 'source manifest too large');
  await putImmutable(send, config.bucket, `${key}/source.zip`, source, 'application/zip');
  await putImmutable(send, config.bucket, `${key}/manifest.json`, manifestBody, 'application/json');
  return { version: 1, kind: 'verified-project-source', project_id: config.project_id, revision_id: config.revision_id,
    bucket: config.bucket, source_key: `${key}/source.zip`, manifest_key: `${key}/manifest.json`,
    source_sha256: sha(source), manifest_sha256: sha(manifestBody), bytes: source.length,
    full_readback_verified: true, deployment_ready: false };
}
export async function fetch(config, expected, send) {
  const key = target(config);
  need(/^[a-f0-9]{64}$/.test(expected.source_sha256) && /^[a-f0-9]{64}$/.test(expected.manifest_sha256), 'trusted revision hashes required');
  const body = await get(send, config.bucket, `${key}/manifest.json`, 1024 * 1024);
  need(sha(body) === expected.manifest_sha256, 'manifest checksum mismatch');
  const manifest = JSON.parse(body);
  const source = await get(send, config.bucket, `${key}/source.zip`, MAX_BYTES);
  need(sha(source) === expected.source_sha256, 'revision checksum mismatch');
  verify(source, manifest);
  return { source, manifest };
}
const MAGIC = Buffer.from('CUBEPRJ1');
function objectKey(master, config) {
  need(Buffer.isBuffer(master) && master.length === 32, '32-byte source encryption key required');
  return Buffer.from(crypto.hkdfSync('sha256', master, Buffer.from('baarcha-project-source-v1'), Buffer.from(config.project_id), 32));
}
function seal(plain, key, context) {
  const nonce = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv('aes-256-gcm', key, nonce);
  cipher.setAAD(Buffer.from(context));
  const encrypted = Buffer.concat([cipher.update(plain), cipher.final()]);
  return Buffer.concat([MAGIC, nonce, cipher.getAuthTag(), encrypted]);
}
function unseal(encrypted, key, context) {
  need(encrypted.length > 36 && encrypted.subarray(0, 8).equals(MAGIC), 'invalid encrypted source format');
  const cipher = crypto.createDecipheriv('aes-256-gcm', key, encrypted.subarray(8, 20));
  cipher.setAAD(Buffer.from(context)); cipher.setAuthTag(encrypted.subarray(20, 36));
  return Buffer.concat([cipher.update(encrypted.subarray(36)), cipher.final()]);
}
async function putEncrypted(send, bucket, object, plain, key) {
  const encrypted = seal(plain, key, `${bucket}/${object}`);
  try {
    await send('PutObject', { Bucket: bucket, Key: object, Body: encrypted,
      ContentType: 'application/octet-stream', IfNoneMatch: '*', ServerSideEncryption: 'AES256',
      ChecksumSHA256: crypto.createHash('sha256').update(encrypted).digest('base64'), Metadata: { sha256: sha(encrypted) } });
  } catch (error) { if (error?.$metadata?.httpStatusCode !== 412) throw error; }
  // Random nonces differ on retries. Verify the existing object's authenticated
  // plaintext, not equality with the newly generated ciphertext.
  const stored = await get(send, bucket, object, plain.length + 36);
  need(unseal(stored, key, `${bucket}/${object}`).equals(plain), 'encrypted immutable source conflict');
  return sha(stored);
}
// Encryption is performed before object storage. This path requires only object
// Get/Put permissions and does not rely on access to bucket administration APIs.
// The existing controller master key stays on the coordinator, never a worker.
export async function publishEncrypted(config, source, manifest, send, master) {
  const base = target(config); verify(source, manifest);
  const key = objectKey(master, config);
  try {
    const body = Buffer.from(JSON.stringify(manifest)); need(body.length <= 1024 * 1024, 'manifest too large');
    const sourceKey = `${base}/source.zip.enc`; const manifestKey = `${base}/manifest.json.enc`;
    const sourceCipherSHA = await putEncrypted(send, config.bucket, sourceKey, source, key);
    const manifestCipherSHA = await putEncrypted(send, config.bucket, manifestKey, body, key);
    return { version: 1, kind: 'verified-encrypted-project-source', encryption: 'aes-256-gcm-hkdf-sha256-v1',
      project_id: config.project_id, revision_id: config.revision_id, bucket: config.bucket,
      source_key: sourceKey, manifest_key: manifestKey, source_sha256: sha(source), manifest_sha256: sha(body),
      source_ciphertext_sha256: sourceCipherSHA, manifest_ciphertext_sha256: manifestCipherSHA,
      bytes: source.length, full_readback_verified: true, deployment_ready: false };
  } finally { key.fill(0); }
}
export async function fetchEncrypted(config, expected, send, master) {
  const base = target(config); const key = objectKey(master, config);
  try {
    need(expected.kind === 'verified-encrypted-project-source' && expected.project_id === config.project_id && expected.revision_id === config.revision_id && expected.bucket === config.bucket, 'encrypted receipt identity differs');
    const sourceKey = `${base}/source.zip.enc`; const manifestKey = `${base}/manifest.json.enc`;
    const body = await get(send, config.bucket, manifestKey, (1024 * 1024) + 36);
    need(sha(body) === expected.manifest_ciphertext_sha256, 'manifest ciphertext differs');
    const manifestBody = unseal(body, key, `${config.bucket}/${manifestKey}`);
    need(sha(manifestBody) === expected.manifest_sha256, 'manifest plaintext differs');
    const sourceBody = await get(send, config.bucket, sourceKey, MAX_BYTES + 36);
    need(sha(sourceBody) === expected.source_ciphertext_sha256, 'source ciphertext differs');
    const source = unseal(sourceBody, key, `${config.bucket}/${sourceKey}`);
    need(sha(source) === expected.source_sha256, 'source plaintext differs');
    const manifest = JSON.parse(manifestBody); verify(source, manifest);
    return { source, manifest };
  } finally { key.fill(0); }
}
async function readPrivate(file, max) {
  const f = await fs.open(file, 'r');
  try { const s = await f.stat(); need(s.isFile() && s.uid === process.getuid() && (s.mode & 0o077) === 0 && s.size <= max, 'private bounded file required'); return await f.readFile(); }
  finally { await f.close(); }
}
async function main() {
  need(process.argv.length === 3, 'usage: source-store.mjs PRIVATE_CONFIG.json');
  const config = JSON.parse(await readPrivate(process.argv[2], 1024 * 1024));
  need(config.operation === 'publish' || config.operation === 'fetch', 'explicit operation required');
  target(config);
  need(typeof config.encryption_key_file === 'string', 'existing controller encryption key file required');
  const master = Buffer.from((await readPrivate(config.encryption_key_file, 1024)).toString().trim(), 'base64');
  need(master.length === 32, 'invalid source encryption key');
  need(/^[a-z]{2}-[a-z]+-\d$/.test(config.region), 'AWS region required');
  const require = createRequire(config.sdk_package ?? '/opt/baarcha/app/landing/package.json');
  const sdk = require('@aws-sdk/client-s3');
  const client = new sdk.S3Client({ region: config.region, endpoint: `https://s3.${config.region}.amazonaws.com`, maxAttempts: 1 });
  const send = (name, input) => client.send(new sdk[name + 'Command'](input));
  try {
    if (config.operation === 'publish') {
      const source = await readPrivate(path.join(config.directory, 'source.zip'), MAX_BYTES);
      const manifest = JSON.parse(await readPrivate(path.join(config.directory, 'manifest.json'), 1024 * 1024));
      const receipt = await publishEncrypted(config, source, manifest, send, master);
      await fs.writeFile(config.receipt, JSON.stringify(receipt, null, 2) + '\n', { mode: 0o600, flag: 'wx' });
      process.stdout.write(JSON.stringify({ readback_verified: true, bytes: receipt.bytes, deployment_ready: false }) + '\n');
    } else {
      const expected = JSON.parse(await readPrivate(config.receipt, 1024 * 1024));
      need(expected.project_id === config.project_id && expected.revision_id === config.revision_id && expected.bucket === config.bucket, 'receipt identity differs');
      const data = await fetchEncrypted(config, expected, send, master);
      await fs.mkdir(config.directory, { mode: 0o700 });
      await fs.writeFile(path.join(config.directory, 'source.zip'), data.source, { mode: 0o600, flag: 'wx' });
      await fs.writeFile(path.join(config.directory, 'manifest.json'), JSON.stringify(data.manifest), { mode: 0o600, flag: 'wx' });
      process.stdout.write(JSON.stringify({ source_verified: true, extracted: false }) + '\n');
    }
  } finally { master.fill(0); client.destroy(); }
}
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main().catch(() => { console.error('Project source transfer failed; no deployment pointer changed.'); process.exitCode = 1; });
}
