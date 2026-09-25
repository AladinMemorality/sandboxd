// Fixed loopback fixture probes. No credentials, arbitrary URLs or paid models.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { fileURLToPath } from 'node:url';

const ORIGIN = 'http://127.0.0.1:3012';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
export function validateInput(mode, output, nonce, cwd = process.cwd()) {
  assert(['serve', 'ready', 'warm', 'load', 'hmr'].includes(mode));
  assert.match(nonce, /^[0-9a-f]{16}$/);
  const root = `/home/sandbox/workspace/app/.operator-frontend-build-20260925/${nonce}/output`;
  assert.equal(cwd, root + '/work');
  if (mode !== 'serve') assert.equal(output, `${root}/${mode}.json`);
  return root;
}
export function percentile(rows, q) {
  assert(rows.length > 0);
  const sorted = [...rows].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * q) - 1)];
}
export function validateItems(body) {
  assert(Array.isArray(body.items) && body.items.length === 256);
  assert(body.items.every((x, index) => x.id === index && x.label === `Fixture item ${index}` && x.category === index % 5));
}
export function matchingUpdate(message) {
  return message?.type === 'update' && Array.isArray(message.updates) && message.updates.some(update =>
    ['/src/profile-marker.ts', '/src/App.tsx'].includes(update.path));
}
async function get(relative) {
  const response = await fetch(ORIGIN + relative, { signal: AbortSignal.timeout(4000) });
  assert.equal(response.status, 200, `loopback ${relative} status`);
  let size = 0; const chunks = [];
  for await (const chunk of response.body) {
    size += chunk.byteLength; assert(size <= 1024 * 1024, 'response limit'); chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}
async function api() {
  const begin = performance.now(); validateItems(JSON.parse(await get('/api/items')));
  return performance.now() - begin;
}
function save(output, value) {
  const fd = fs.openSync(output, 'wx', 0o600);
  try { fs.writeFileSync(fd, JSON.stringify(value, null, 2) + '\n'); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
}
async function ready() {
  const begin = performance.now(); let last;
  while (performance.now() - begin < 40000) {
    try {
      assert.match(await get('/'), /src\/main.tsx/);
      await get('/@vite/client'); await get('/src/main.tsx'); await get('/src/App.tsx');
      await get('/src/profile-marker.ts');
      // Force normal module transforms; two bounded streams, no browser claim.
      for (let i = 0; i < 48; i += 2) await Promise.all([get(`/src/profile/feature${i}.ts`), get(`/src/profile/feature${i + 1}.ts`)]);
      await api(); return { elapsed_ms: performance.now() - begin, transformed_feature_modules: 48, api_valid: true };
    } catch (error) { last = error; await sleep(250); }
  }
  throw last || Error('readiness timeout');
}
async function apiLoad(count) {
  const begin = performance.now(), samples = [];
  for (let i = 0; i < count; i++) {
    const planned = i === 0 ? begin : performance.now() + 250;
    await sleep(Math.max(0, planned - performance.now()));
    samples.push(await api());
  }
  return { started_wall_ms: Date.now() - (performance.now() - begin), elapsed_ms: performance.now() - begin,
    requests: count, concurrency: 1, starts_per_second_max: 4, failures: 0,
    latency_median_ms: percentile(samples, .5), latency_p95_ms: percentile(samples, .95), latency_max_ms: Math.max(...samples) };
}
async function hmr(nonce) {
  // Vite versions with token-protected HMR expose the token to their local
  // client. It remains inside this process and is never written into reports.
  const client = await get('/@vite/client');
  const token = client.match(/const wsToken\s*=\s*["']([A-Za-z0-9_-]+)["']/)?.[1];
  const ws = new WebSocket(`ws://127.0.0.1:3012/${token ? '?token=' + encodeURIComponent(token) : ''}`, 'vite-hmr');
  let changedAt = 0;
  try {
    const update = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(Error('HMR deadline')), 10000);
      const fail = error => { clearTimeout(timer); reject(error); };
      ws.addEventListener('error', () => fail(Error('HMR socket error')));
      ws.addEventListener('message', event => {
        try {
          const message = JSON.parse(String(event.data));
          if (message.type === 'connected' && !changedAt) {
            const file = path.resolve('src/profile-marker.ts');
            assert.equal(fs.realpathSync(file), file);
            assert.equal(fs.readFileSync(file, 'utf8'), `export const marker = "${nonce}-before";\n`);
            changedAt = performance.now();
            fs.writeFileSync(file, `export const marker = "${nonce}-after";\n`, { flag: 'r+' });
          }
          if (changedAt && matchingUpdate(message)) { clearTimeout(timer); resolve(performance.now() - changedAt); }
        } catch (error) { fail(error); }
      });
    });
    assert((await get('/src/profile-marker.ts')).includes(`${nonce}-after`));
    return { websocket_update_ms: update, transformed_marker_verified: true, browser_render_verified: false };
  } finally { ws.close(); }
}
export async function runMode(mode, output, nonce) {
  validateInput(mode, output, nonce);
  if (mode === 'serve') {
    const items = Array.from({ length: 256 }, (_, id) => ({ id, label: `Fixture item ${id}`, category: id % 5 }));
    const bytes = JSON.stringify({ items });
    const server = http.createServer((req, res) => {
      if (req.method !== 'GET' || req.url !== '/api/items') { res.writeHead(404); res.end(); return; }
      res.writeHead(200, { 'content-type': 'application/json', 'cache-control': 'no-store', 'content-length': Buffer.byteLength(bytes) });
      res.end(bytes);
    });
    server.headersTimeout = 5000; server.requestTimeout = 5000; server.maxConnections = 4;
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(3013, '127.0.0.1', resolve); });
    return;
  }
  const result = mode === 'ready' ? await ready() : mode === 'hmr' ? await hmr(nonce) : await apiLoad(mode === 'load' ? 64 : 8);
  save(output, { mode, ...result });
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const timer = setTimeout(() => process.exit(2), process.argv[2] === 'serve' ? 500000 : 60000); timer.unref();
  runMode(...process.argv.slice(2)).catch(() => { console.error('Fixed loopback profile probe failed'); process.exitCode = 1; });
}
