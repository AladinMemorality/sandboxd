import { test } from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { Readable } from 'node:stream';
import { publish, fetch, publishEncrypted, fetchEncrypted } from './source-store.mjs';
const hash = b => crypto.createHash('sha256').update(b).digest('hex');
const config = { version: 1, project_id: 'project-a', revision_id: 'revision-a', bucket: 'private-fixture', prefix: 'cube-private' };
const source = Buffer.from('fixture archive; ZIP validation belongs to the Go packager');
const manifest = { version: 1, kind: 'private-project-source', files: [{ path: 'index.html' }], excluded: [], bytes: source.length, source_sha256: hash(source), dependency_key: 'a'.repeat(64) };
function store() {
  const objects = new Map();
  const send = async (op, input) => {
    if (op === 'GetPublicAccessBlock') return { PublicAccessBlockConfiguration: { BlockPublicAcls: true, IgnorePublicAcls: true, BlockPublicPolicy: true, RestrictPublicBuckets: true } };
    if (op === 'PutObject') {
      assert.equal(input.IfNoneMatch, '*'); assert.equal(input.ServerSideEncryption, 'AES256'); assert.equal(input.ACL, undefined);
      assert.equal(input.ChecksumSHA256, crypto.createHash('sha256').update(input.Body).digest('base64'));
      if (objects.has(input.Key)) throw { $metadata: { httpStatusCode: 412 } };
      objects.set(input.Key, Buffer.from(input.Body)); return {};
    }
    assert.equal(op, 'GetObject'); const body = objects.get(input.Key); if (!body) throw Error('missing');
    return { ContentLength: body.length, Body: Readable.from([body]) };
  };
  return { objects, send };
}
test('immutable publish, retry and full readback retain exact source', async () => {
  const s = store(); const receipt = await publish(config, source, manifest, s.send);
  assert.equal(receipt.deployment_ready, false);
  assert.deepEqual(await publish(config, source, manifest, s.send), receipt);
  const restored = await fetch(config, receipt, s.send);
  assert.deepEqual(restored.source, source); assert.deepEqual(restored.manifest, manifest);
});
test('conflicting retry never overwrites source or advances a pointer', async () => {
  const s = store(); await publish(config, source, manifest, s.send);
  const different = Buffer.from('different');
  await assert.rejects(publish(config, different, { ...manifest, bytes: different.length, source_sha256: hash(different) }, s.send), /conflict|length/);
  assert.deepEqual([...s.objects.values()][0], source);
});
test('corrupt source and changed manifest are rejected on fetch', async () => {
  const s = store(); const receipt = await publish(config, source, manifest, s.send);
  s.objects.set(receipt.source_key, Buffer.from('bad'));
  await assert.rejects(fetch(config, receipt, s.send), /checksum/);
  s.objects.set(receipt.manifest_key, Buffer.from('{}'));
  await assert.rejects(fetch(config, receipt, s.send), /manifest checksum/);
});
test('namespace traversal and source mismatch fail before network calls', async () => {
  const send = () => assert.fail('unexpected request');
  await assert.rejects(publish({ ...config, project_id: '../other' }, source, manifest, send), /identity/);
  await assert.rejects(publish(config, source, { ...manifest, source_sha256: 'b'.repeat(64) }, send), /checksum/);
});
test('uncertain upload preserves error for reconciliation', async () => {
  await assert.rejects(publish(config, source, manifest, async () => { throw Error('timeout'); }), /timeout/);
});
test('public bucket configuration is rejected before any object write', async () => {
  await assert.rejects(publish(config, source, manifest, async name => {
    assert.equal(name, 'GetPublicAccessBlock'); return { PublicAccessBlockConfiguration: {} };
  }), /block all public access/);
});
test('encrypted objects work with object-only credentials and preserve idempotency', async () => {
  const s = store(); const master = crypto.randomBytes(32);
  const send = (name, input) => { assert.notEqual(name, 'GetPublicAccessBlock'); return s.send(name, input); };
  const receipt = await publishEncrypted(config, source, manifest, send, master);
  assert.deepEqual(await publishEncrypted(config, source, manifest, send, master), receipt);
  for (const stored of s.objects.values()) assert.equal(stored.includes(source), false);
  assert.deepEqual((await fetchEncrypted(config, receipt, send, master)).source, source);
  await assert.rejects(fetchEncrypted(config, receipt, send, crypto.randomBytes(32)));
});
test('encrypted objects authenticate project identity and immutable content', async () => {
  const s = store(); const master = crypto.randomBytes(32);
  const receipt = await publishEncrypted(config, source, manifest, s.send, master);
  const replacement = Buffer.from('modified');
  await assert.rejects(publishEncrypted(config, replacement, { ...manifest, bytes: replacement.length, source_sha256: hash(replacement) }, s.send, master));
  await assert.rejects(fetchEncrypted({ ...config, project_id: 'someone-else' }, receipt, s.send, master), /identity/);
  const ciphertext = s.objects.get(receipt.source_key); ciphertext[ciphertext.length - 1] ^= 1;
  await assert.rejects(fetchEncrypted(config, receipt, s.send, master), /ciphertext differs/);
});
