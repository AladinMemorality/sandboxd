// Temporary encrypted operator test copies. Remove all objects after acceptance.
// This is not the permanent source/lockfile deployment store.
// Encrypt each private workspace with a fresh transfer key; the
// worker receives a short-lived GET URL and that key, never AWS credentials.
import fs from 'node:fs';
import fsp from 'node:fs/promises';
import crypto from 'node:crypto';
import { pipeline } from 'node:stream/promises';
import { createRequire } from 'node:module';
const require = createRequire('/opt/baarcha/app/landing/package.json');
const { S3Client, PutObjectCommand, HeadObjectCommand } = require('@aws-sdk/client-s3');
const { SignatureV4 } = require('@smithy/signature-v4');
class Hash {
  constructor(secret) { this.hash = secret ? crypto.createHmac('sha256', secret) : crypto.createHash('sha256'); }
  update(data) { this.hash.update(data); }
  async digest() { return this.hash.digest(); }
}
async function main() {
  process.umask(0o077);
  const [file, manifestPath, receiptPath] = process.argv.slice(2);
  if (!file || !manifestPath || !receiptPath || process.getuid() !== 0) throw Error('root-only operator arguments required');
  const manifest = JSON.parse(await fsp.readFile(manifestPath, 'utf8'));
  if (!/^[0-9A-HJKMNP-TV-Z]{26}$/.test(manifest.sandbox_id) || !/^[a-f0-9]{64}$/.test(manifest.sha256)) throw Error('invalid artifact identity');
  const stat = await fsp.stat(file);
  if (!stat.isFile() || stat.size > 4 * 1024 ** 3 || stat.uid !== 0 || (stat.mode & 0o077)) throw Error('private bounded artifact required');
  const key = crypto.randomBytes(32), iv = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv('aes-256-gcm', key, iv);
  cipher.setAAD(Buffer.from(manifest.sha256));
  const encrypted = file + '.enc';
  await pipeline(fs.createReadStream(file), cipher, fs.createWriteStream(encrypted, {mode:0o600, flags:'wx'}));
  const region = 'eu-central-1', bucket = 'punicas';
  const relocation = manifest.kind === 'project-relocation-v1';
  if (relocation && !['workspace','home','history'].includes(manifest.role)) throw Error('invalid relocation role');
  const objectKey = `baarcha/cube/${relocation ? 'project-moves' : 'test-copies'}/${manifest.sha256}/${crypto.randomUUID()}.zip.enc`;
  const client = new S3Client({region, maxAttempts:2});
  try {
    await client.send(new PutObjectCommand({Bucket:bucket, Key:objectKey,
      Body:fs.createReadStream(encrypted), ContentLength:stat.size,
      ContentType:'application/octet-stream', ServerSideEncryption:'AES256', IfNoneMatch:'*'}));
    const head = await client.send(new HeadObjectCommand({Bucket:bucket, Key:objectKey}));
    if (head.ContentLength !== stat.size) throw Error('object length differs');
    const credentials = await client.config.credentials();
    const signer = new SignatureV4({credentials, region, service:'s3', sha256:Hash, uriEscapePath:false});
    const host = `${bucket}.s3.${region}.amazonaws.com`;
    const signed = await signer.presign({method:'GET', protocol:'https:', hostname:host,
      path:'/'+objectKey, headers:{host, 'x-amz-content-sha256':'UNSIGNED-PAYLOAD'}, query:{}}, {expiresIn:21600});
    const query = new URLSearchParams();
    for (const [k,v] of Object.entries(signed.query)) for (const value of Array.isArray(v) ? v : [v]) query.append(k,value);
    const receipt = {...manifest, workspace_sha256:manifest.sha256, bucket, object_key:objectKey, version_id:head.VersionId, url:`https://${host}/${objectKey}?${query}`,
      key:key.toString('base64'), iv:iv.toString('base64'), tag:cipher.getAuthTag().toString('base64'),
      compressed_bytes:stat.size};
    await fsp.writeFile(receiptPath, JSON.stringify(receipt), {mode:0o600, flag:'wx'});
    console.log(JSON.stringify({artifact:manifest.sandbox_id, uploaded_bytes:stat.size}));
  } finally {key.fill(0); client.destroy(); await fsp.unlink(encrypted).catch(() => {});}
}
main().catch(e => {console.error('Private test publication failed:', e.name); process.exitCode=1;});
