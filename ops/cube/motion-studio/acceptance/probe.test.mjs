import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {exercise, FILE_LIMIT, videoAtLimit, writeReceipt} from './probe.mjs';
import {cleanup} from './cleanup.mjs';
const id = '11111111-1111-4111-8111-111111111111';
const customer = '22222222-2222-4222-8222-222222222222';
const marker = 'cube-motion-owned-' + 'a'.repeat(32);
const seed = Buffer.alloc(32); seed.writeUInt32BE(32); seed.write('ftyp', 4);
const digest = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');

test('HTTP acceptance covers exact byte limit, unchanged oversize result and decoded range/download bytes', async () => {
  const receipts = new Map(), calls = [];
  let project, savedVideo;
  const response = (status, value, headers = {}) => ({status, headers, body: Buffer.isBuffer(value) ? value : Buffer.from(JSON.stringify(value))});
  const request = async (method, route, body, headers = {}) => {
    calls.push(method + ' ' + route);
    if (route === '/api/status') return response(200, {mode: 'shared-workspace'});
    if (route === '/api/projects' && method === 'GET') return response(200, {projects: project ? [project] : []});
    if (route === '/api/projects' && method === 'POST') {
      project = {id, brief: JSON.parse(body), assets: [], jobs: [], renders: []};
      return response(201, project);
    }
    if (route === '/api/projects/' + id && method === 'PATCH') {
      project.brief = JSON.parse(body).brief; return response(200, project);
    }
    if (route === '/api/projects/' + id && method === 'GET') return response(200, project);
    if (route.endsWith('/assets') && method === 'POST') {
      const begin = body.indexOf(Buffer.from('Content-Type: video/mp4\r\n\r\n')) + 'Content-Type: video/mp4\r\n\r\n'.length;
      const end = body.lastIndexOf(Buffer.from('\r\n--cube-owned-motion-acceptance-boundary--\r\n'));
      const bytes = body.subarray(begin, end);
      if (bytes.length > FILE_LIMIT) return response(413, {error: 'large'});
      assert.equal(bytes.length, FILE_LIMIT); savedVideo = Buffer.from(bytes);
      project.assets = [{file: id + '.mp4', kind: 'media', duration: 1}]; return response(201, project);
    }
    if (route.startsWith('/media/' + id + '/')) {
      if (method === 'HEAD') return response(200, Buffer.alloc(0), {'content-length': FILE_LIMIT, 'accept-ranges': 'bytes'});
      if (headers.range) {
        const first = headers.range.startsWith('bytes=-') ? FILE_LIMIT - 1024 : 0;
        return response(206, savedVideo.subarray(first, first + 1024), {'content-range': `bytes ${first}-${first + 1023}/${FILE_LIMIT}`});
      }
      return response(200, savedVideo, {'content-disposition': 'attachment; filename="owned.mp4"'});
    }
    throw Error('Unexpected or paid route: ' + method + ' ' + route);
  };
  let decoded = false;
  await exercise({request, seed, marker, receipt: async (name, value) => {assert.ok(!receipts.has(name)); receipts.set(name, value);}, decode: async video => {
    assert.equal(video.length, FILE_LIMIT); assert.equal(video.toString('ascii', seed.length + 4, seed.length + 8), 'free'); decoded = true;
  }});
  assert.ok(decoded); assert.equal(receipts.get('http-complete.json').paid_jobs_submitted, 0);
  assert.equal(calls.filter(c => c.endsWith('/assets')).length, 2);
  assert.equal(receipts.get('oversize-refused.json').project_unchanged, true);
});

test('exact-limit padding retains the MP4 and adds one valid free atom', () => {
  const video = videoAtLimit(seed);
  assert.equal(video.length, FILE_LIMIT); assert.deepEqual(video.subarray(0, seed.length), seed);
  assert.equal(video.readUInt32BE(seed.length), FILE_LIMIT - seed.length);
  assert.throws(() => videoAtLimit(Buffer.from('not video')));
});

async function fixture(t) {
  const temp = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'motion-owned-')));
  t.after(() => fs.rm(temp, {recursive: true, force: true}));
  const root = path.join(temp, 'worker'), directory = path.join(temp, 'receipts');
  await fs.mkdir(root); await fs.mkdir(directory);
  const old = {id: customer, brief: {brand: 'Customer film'}, jobs: [{status: 'succeeded'}], renders: []};
  await fs.mkdir(path.join(root, customer)); await fs.writeFile(path.join(root, customer, 'project.json'), JSON.stringify(old, null, 2));
  await writeReceipt(directory, 'worker-baseline.json', {[customer]: digest(old)});
  await writeReceipt(directory, 'film-create-intent.json', {marker, existing: [customer]});
  await writeReceipt(directory, 'http-failure.json', {last_request: {method: 'POST', route: '/api/projects', status: 201}});
  await fs.mkdir(path.join(root, id));
  await fs.writeFile(path.join(root, id, 'project.json'), JSON.stringify({id, brief: {brand: marker}, jobs: [], renders: []}));
  return {root, directory, old};
}
test('cleanup resolves an acknowledged create with a lost body by its owned marker and preserves customer bytes', async t => {
  const {root, directory} = await fixture(t);
  const before = await fs.readFile(path.join(root, customer, 'project.json'));
  await cleanup(root, directory);
  assert.deepEqual(await fs.readdir(root), [customer]);
  assert.deepEqual(await fs.readFile(path.join(root, customer, 'project.json')), before);
  assert.equal(JSON.parse(await fs.readFile(path.join(directory, 'film-cleanup.json'))).films_removed, 1);
});
test('cleanup preserves the fixture after an interrupted worker request without acknowledgement', async t => {
  const {root, directory} = await fixture(t);
  await fs.writeFile(path.join(directory, 'http-failure.json'), JSON.stringify({last_request: {method: 'POST', route: '/api/projects'}}));
  await assert.rejects(cleanup(root, directory), /outcome is uncertain/);
  assert.ok((await fs.stat(path.join(root, id, 'project.json'))).isFile());
});
test('cleanup refuses a changed customer project before deleting any fixture', async t => {
  const {root, directory, old} = await fixture(t);
  await fs.writeFile(path.join(root, customer, 'project.json'), JSON.stringify({...old, changed: true}));
  await assert.rejects(cleanup(root, directory), /Existing worker project changed/);
  assert.ok((await fs.stat(path.join(root, id))).isDirectory());
});
test('cleanup refuses a foreign extra film and symlink redirection', async t => {
  const {root, directory} = await fixture(t);
  await fs.writeFile(path.join(root, id, 'project.json'), JSON.stringify({id, brief: {brand: 'Someone else'}, jobs: [], renders: []}));
  await assert.rejects(cleanup(root, directory));
  await fs.rename(path.join(root, id), path.join(root, 'retained'));
  await fs.symlink(path.join(root, 'retained'), path.join(root, id));
  await assert.rejects(cleanup(root, directory));
  assert.ok((await fs.stat(path.join(root, 'retained', 'project.json'))).isFile());
});
test('receipts cannot be overwritten or escape their directory', async t => {
  const {directory} = await fixture(t);
  await assert.rejects(writeReceipt(directory, 'film-create-intent.json', {}), {code: 'EEXIST'});
  await assert.rejects(writeReceipt(directory, '../outside.json', {}));
});
