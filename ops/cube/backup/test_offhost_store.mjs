import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { RECIPIENT, transfer } from './offhost_store.mjs';

async function fixture(t, size = 11 * 1024 * 1024) {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'cube-offhost-unit-'))); await fs.chmod(root, 0o700);
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const bytes = Buffer.alloc(size, 73); const cipher = path.join(root, 'sealed.gpg');
  await fs.writeFile(cipher, bytes, { mode: 0o600 });
  const sha = crypto.createHash('sha256').update(bytes).digest('hex');
  const manifest = path.join(root, 'seal.json');
  await fs.writeFile(manifest, JSON.stringify({ version: 1, kind: 'encrypted-cold-cube-pair', captured_at: 1000, sealed_at: 1001, recipient_fingerprint: RECIPIENT, ciphertext_sha256: sha, capture_manifest_sha256: 'a'.repeat(64), ciphertext_path: cipher, file_identity: { bytes: size } }), { mode: 0o600 });
  return { root, bytes, cipher, sha, config: { version: 1, manifest, output: path.join(root, 'new-job'), bucket: 'fixture-only', prefix: 'reviewed_backups', readback_only: false } };
}
function fake(f, options = {}) {
  const calls = []; const parts = [];
  return { calls, send: async (name, value) => {
    calls.push({ name, value });
    if (name === 'CreateMultipartUpload') return { UploadId: 'owned-upload' };
    if (name === 'UploadPart') { assert.equal(value.ContentMD5, crypto.createHash('md5').update(value.Body).digest('base64')); parts.push(Buffer.from(value.Body)); return { ETag: 'part-' + value.PartNumber }; }
    if (name === 'CompleteMultipartUpload') { assert.equal(value.IfNoneMatch, '*'); if (options.failComplete) throw Error('uncertain completion'); assert.deepEqual(Buffer.concat(parts), f.bytes); return {}; }
    if (name === 'GetObject') {
      const data = options.badReadback ? Buffer.alloc(f.bytes.length, 2) : f.bytes;
      return { ContentLength: options.oversized ? f.bytes.length + 1 : f.bytes.length, Body: (async function* () { yield data.subarray(0, 103); yield data.subarray(103); })() };
    }
    throw Error('Unexpected external operation');
  } };
}
test('bounded multipart has explicit no-overwrite completion and complete GET hash evidence', async t => {
  const f = await fixture(t); const s = fake(f);
  const result = await transfer(f.config, s.send, { partBytes: 5 * 1024 * 1024 });
  assert.equal(s.calls.filter(c => c.name === 'UploadPart').length, 3);
  assert.equal(result.ciphertext_sha256, f.sha); assert.equal(result.application_restore_verified, false);
  assert.deepEqual(await fs.readFile(path.join(f.config.output, 'readback.gpg')), f.bytes);
  assert.equal((await fs.stat(path.join(f.config.output, 'copy-evidence.json'))).mode & 0o777, 0o600);
  const receipt = JSON.parse(await fs.readFile(path.join(f.config.output, 'offhost-receipt.json')));
  assert.equal(receipt.kind, 'offhost-copy'); assert.equal(receipt.offhost, true);
  assert.equal(receipt.evidence_sha256, crypto.createHash('sha256').update(await fs.readFile(path.join(f.config.output, 'copy-evidence.json'))).digest('hex'));
});
test('uncertain completion retains journal and never retries, deletes or claims verified', async t => {
  const f = await fixture(t, 1024); const s = fake(f, { failComplete: true });
  await assert.rejects(transfer(f.config, s.send));
  assert.deepEqual(s.calls.map(c => c.name), ['CreateMultipartUpload', 'UploadPart', 'CompleteMultipartUpload']);
  assert.ok((await fs.readdir(f.config.output)).some(n => n.startsWith('phase-')));
  await assert.rejects(fs.stat(path.join(f.config.output, 'copy-evidence.json')));
  await assert.rejects(fs.stat(path.join(f.config.output, 'offhost-receipt.json')));
});
test('fresh readback-only job reconciles already completed object without new upload', async t => {
  const f = await fixture(t, 1024); f.config.readback_only = true; const s = fake(f);
  await transfer(f.config, s.send); assert.deepEqual(s.calls.map(c => c.name), ['GetObject']);
});
for (const options of [{ badReadback: true }, { oversized: true }]) test('tampered or oversized GET cannot publish verified copy', async t => {
  const f = await fixture(t, 1024); const s = fake(f, options);
  await assert.rejects(transfer(f.config, s.send));
  await assert.rejects(fs.stat(path.join(f.config.output, 'readback.gpg')));
  await assert.rejects(fs.stat(path.join(f.config.output, 'copy-evidence.json')));
});
test('source tampering fails before any remote mutation', async t => {
  const f = await fixture(t, 1024); await fs.writeFile(f.cipher, Buffer.alloc(1024, 1)); const s = fake(f);
  await assert.rejects(transfer(f.config, s.send)); assert.equal(s.calls.length, 0);
});
test('source changed after a part acknowledgement prevents completion', async t => {
  const f = await fixture(t, 1024); const s = fake(f);
  const send = async (name, value) => {
    const response = await s.send(name, value);
    if (name === 'UploadPart') await fs.writeFile(f.cipher, Buffer.alloc(1024, 9));
    return response;
  };
  await assert.rejects(transfer(f.config, send));
  assert.ok(!s.calls.some(c => c.name === 'CompleteMultipartUpload' || c.name === 'GetObject'));
});
test('existing job, unsafe prefix, wrong recipient and symlink inputs are refused', async t => {
  const f = await fixture(t, 1024); const s = fake(f);
  await fs.mkdir(f.config.output, { mode: 0o700 }); await assert.rejects(transfer(f.config, s.send));
  await assert.rejects(transfer({ ...f.config, output: path.join(f.root, 'two'), prefix: '../wrong' }, s.send));
  const m = JSON.parse(await fs.readFile(f.config.manifest)); m.recipient_fingerprint = 'B'.repeat(40); await fs.writeFile(f.config.manifest, JSON.stringify(m));
  await assert.rejects(transfer({ ...f.config, output: path.join(f.root, 'three') }, s.send));
  const link = path.join(f.root, 'link.json'); await fs.symlink(f.config.manifest, link);
  await assert.rejects(transfer({ ...f.config, manifest: link, output: path.join(f.root, 'four') }, s.send)); assert.equal(s.calls.length, 0);
});
