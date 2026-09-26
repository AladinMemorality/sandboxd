// Native operator probe. Every request goes through the owned Cube guest's
// application port; no direct worker request can satisfy these checks.
import assert from 'node:assert/strict';
import http from 'node:http';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';

const exec = promisify(execFile);
export const FILE_LIMIT = 50 * 1024 * 1024;
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
const hash = b => createHash('sha256').update(b).digest('hex');
const json = r => JSON.parse(r.body.toString());

export function videoAtLimit(seed) {
  assert.equal(seed.toString('ascii', 4, 8), 'ftyp');
  assert.ok(seed.length >= 24 && seed.length < FILE_LIMIT - 8);
  const video = Buffer.alloc(FILE_LIMIT);
  seed.copy(video);
  video.writeUInt32BE(FILE_LIMIT - seed.length, seed.length);
  video.write('free', seed.length + 4, 'ascii');
  return video;
}

export function multipart(video) {
  const boundary = 'cube-owned-motion-acceptance-boundary';
  return {
    contentType: `multipart/form-data; boundary=${boundary}`,
    body: Buffer.concat([
      Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="kind"\r\n\r\nmedia\r\n--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="owned-fixture.mp4"\r\nContent-Type: video/mp4\r\n\r\n`),
      video, Buffer.from(`\r\n--${boundary}--\r\n`),
    ]),
  };
}

// Native ingress is fixed by the maintenance runner. Disable proxy discovery,
// redirects and implicit retries; a POST with an uncertain outcome stays so.
export function transport(config) {
  assert.equal(config.origin, 'http://127.0.0.1:20080');
  assert.match(config.host, /^3000-[a-f0-9]{32}\.cube\.app$/);
  assert.ok(typeof config.trafficToken === 'string' && config.trafficToken.length > 0);
  return (method, route, body, headers = {}) => new Promise((resolve, reject) => {
    const request = http.request(config.origin + route, {method, headers: {
      host: config.host, 'cube-traffic-access-token': config.trafficToken,
      origin: 'https://cube-motion-acceptance.invalid', connection: 'close',
      ...headers, ...(body ? {'content-length': body.length} : {}),
    }}, response => {
      const chunks = []; let bytes = 0;
      response.on('data', chunk => {
        bytes += chunk.length;
        if (bytes > FILE_LIMIT + 1024) return response.destroy(new Error('Response exceeded fixture bound'));
        chunks.push(chunk);
      });
      response.on('error', reject);
      response.on('end', () => resolve({status: response.statusCode, headers: response.headers, body: Buffer.concat(chunks)}));
    });
    request.setTimeout(125_000, () => request.destroy(new Error('Owned guest request timed out')));
    request.on('error', reject);
    request.end(body);
  });
}

export async function writeReceipt(directory, name, value) {
  assert.match(name, /^[a-z0-9-]+\.json$/);
  const file = await fs.open(path.join(directory, name), 'wx', 0o600);
  try { await file.writeFile(JSON.stringify(value, null, 2) + '\n'); await file.sync(); }
  finally { await file.close(); }
  const parent = await fs.open(directory, 'r');
  try { await parent.sync(); } finally { await parent.close(); }
}

export async function exercise({request, seed, marker, receipt, decode}) {
  assert.match(marker, /^cube-motion-owned-[a-f0-9]{32}$/);
  let response = await request('GET', '/api/status');
  assert.equal(response.status, 200); assert.equal(json(response).mode, 'shared-workspace');
  response = await request('GET', '/api/projects');
  assert.equal(response.status, 200);
  const before = json(response).projects;
  assert.ok(Array.isArray(before));
  const baseline = new Map();
  for (const project of before) {
    assert.match(project.id, UUID);
    const r = await request('GET', `/api/projects/${project.id}`);
    assert.equal(r.status, 200);
    baseline.set(project.id, hash(r.body));
  }
  await receipt('worker-baseline.json', Object.fromEntries(baseline));
  const brief = {brand: marker, brief: 'Owned Cube connection verification. No generated media or paid jobs.', duration: 6, music: 'none', narration: 'none', generateFootage: false};
  await receipt('film-create-intent.json', {marker, existing: [...baseline.keys()]});
  response = await request('POST', '/api/projects', Buffer.from(JSON.stringify(brief)), {'content-type': 'application/json'});
  assert.equal(response.status, 201);
  const project = json(response);
  assert.match(project.id, UUID); assert.equal(project.brief.brand, marker);
  assert.ok(!baseline.has(project.id));
  await receipt('film-created.json', {id: project.id, marker});
  const route = `/api/projects/${project.id}`;
  response = await request('PATCH', route, Buffer.from(JSON.stringify({brief: {...brief, facts: 'Updated through the Cube guest'}})), {'content-type': 'application/json'});
  assert.equal(response.status, 200);
  assert.equal(json(response).brief.facts, 'Updated through the Cube guest');
  assert.deepEqual(json(response).jobs, []); assert.deepEqual(json(response).renders, []);
  const video = videoAtLimit(seed);
  let upload = multipart(video);
  response = await request('POST', route + '/assets', upload.body, {'content-type': upload.contentType});
  assert.equal(response.status, 201);
  const updated = json(response);
  assert.equal(updated.assets.length, 1);
  const asset = updated.assets[0];
  assert.match(asset.file, /^[a-f0-9-]{36}\.mp4$/);
  assert.equal(asset.kind, 'media'); assert.ok(asset.duration >= 0.5);
  const media = `/media/${project.id}/${asset.file}`;
  await receipt('upload-50mib.json', {id: project.id, file: asset.file, bytes: video.length, sha256: hash(video)});

  upload = multipart(Buffer.concat([video, Buffer.from([0])]));
  response = await request('POST', route + '/assets', upload.body, {'content-type': upload.contentType});
  assert.equal(response.status, 413);
  response = await request('GET', route);
  assert.equal(response.status, 200);
  assert.deepEqual(json(response), updated, 'Oversize upload changed the project');
  await receipt('oversize-refused.json', {status: 413, project_unchanged: true});

  response = await request('HEAD', media);
  assert.equal(response.status, 200); assert.equal(Number(response.headers['content-length']), FILE_LIMIT);
  assert.equal(response.headers['accept-ranges'], 'bytes'); assert.equal(response.body.length, 0);
  for (const [range, first, last] of [['bytes=0-1023', 0, 1023], ['bytes=-1024', FILE_LIMIT - 1024, FILE_LIMIT - 1]]) {
    response = await request('GET', media, undefined, {range});
    assert.equal(response.status, 206);
    assert.equal(response.headers['content-range'], `bytes ${first}-${last}/${FILE_LIMIT}`);
    assert.deepEqual(response.body, video.subarray(first, last + 1));
  }
  response = await request('GET', media + '?download=1');
  assert.equal(response.status, 200);
  assert.match(response.headers['content-disposition'], /^attachment;/);
  assert.equal(response.body.length, FILE_LIMIT); assert.equal(hash(response.body), hash(video));
  await decode(response.body);
  await receipt('media-verified.json', {head: true, prefix_range: true, suffix_range: true, download_sha256: hash(video), decoded: true});
  for (const [id, digest] of baseline) {
    response = await request('GET', `/api/projects/${id}`);
    assert.equal(response.status, 200); assert.equal(hash(response.body), digest, 'Existing worker project changed');
  }
  response = await request('GET', '/api/projects');
  assert.equal(response.status, 200);
  assert.deepEqual(json(response).projects.map(p => p.id).sort(), [...baseline.keys(), project.id].sort());
  await receipt('http-complete.json', {success: true, id: project.id, marker, existing_projects_unchanged: baseline.size, paid_jobs_submitted: 0});
}

async function main() {
  // Credentials arrive on a private pipe, never argv or stdout.
  const chunks = []; for await (const chunk of process.stdin) chunks.push(chunk);
  const config = JSON.parse(Buffer.concat(chunks).toString());
  const receipt = (name, value) => writeReceipt(config.directory, name, value);
  const seed = await fs.readFile(config.video);
  const send = transport(config);
  let lastRequest;
  try { await exercise({request: async (method, route, body, headers) => {
    lastRequest = {method, route};
    const response = await send(method, route, body, headers);
    lastRequest.status = response.status;
    return response;
  }, seed, marker: config.marker, receipt,
    decode: async bytes => {
      const target = path.join(config.directory, 'download.mp4');
      await fs.writeFile(target, bytes, {flag: 'wx', mode: 0o600});
      await exec('/usr/bin/ffmpeg', ['-v', 'error', '-i', target, '-frames:v', '1', '-f', 'null', '-'], {timeout: 30_000, maxBuffer: 64 * 1024});
    },
  }); } catch (error) {
    await receipt('http-failure.json', {name: error.name, code: error.code, last_request: lastRequest});
    throw error;
  }
}
if (process.argv[1] === fileURLToPath(import.meta.url)) main().catch(() => {
  // Detailed response bodies can contain customer data. Private phase receipts
  // locate a failure without copying them into service logs.
  console.error('Owned Motion HTTP acceptance failed; inspect private phase receipts.');
  process.exitCode = 1;
});
