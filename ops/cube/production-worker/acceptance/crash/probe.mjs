// Synthetic owned-guest acceptance only. No power operation or recovery fallback.
import fs from 'node:fs/promises';
import {constants as C} from 'node:fs';
import {randomBytes, timingSafeEqual} from 'node:crypto';
import {createServer} from 'node:http';
import os from 'node:os';
import path from 'node:path';
import {pathToFileURL} from 'node:url';

const hex = /^[a-f0-9]{32}$/;
export function validateMarker(value, fixture) {
  return value && value.fixture === fixture && hex.test(fixture) &&
    ['baseline', 'latest'].includes(value.phase) && hex.test(value.nonce) &&
    Object.keys(value).sort().join(',') === 'fixture,nonce,phase';
}
export function sameMarker(a, b) {
  return !!a && !!b && a.fixture === b.fixture && a.phase === b.phase && a.nonce === b.nonce;
}
async function privateDirectory(directory) {
  try { await fs.mkdir(directory, {mode: 0o700}); }
  catch (error) { if (error.code !== 'EEXIST') throw error; }
  const info = await fs.lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink() || info.uid !== process.getuid() || (info.mode & 0o077)) {
    throw new Error('private marker directory required');
  }
}
export async function writeMarker(directory, value) {
  await privateDirectory(directory);
  const temporary = path.join(directory, `.pending-${randomBytes(16).toString('hex')}`);
  const handle = await fs.open(temporary, C.O_WRONLY | C.O_CREAT | C.O_EXCL | C.O_NOFOLLOW, 0o600);
  try { await handle.writeFile(JSON.stringify(value)); await handle.sync(); }
  finally { await handle.close(); }
  await fs.rename(temporary, path.join(directory, 'marker.json'));
  const dir = await fs.open(directory, C.O_RDONLY | C.O_DIRECTORY | C.O_NOFOLLOW);
  try { await dir.sync(); } finally { await dir.close(); }
  // Also persist the directory entry when the directory was newly created.
  const parent = await fs.open(path.dirname(directory), C.O_RDONLY | C.O_DIRECTORY | C.O_NOFOLLOW);
  try { await parent.sync(); } finally { await parent.close(); }
}
export async function readMarker(directory) {
  const file = await fs.open(path.join(directory, 'marker.json'), C.O_RDONLY | C.O_NOFOLLOW);
  try {
    const info = await file.stat();
    if (!info.isFile() || info.size > 4096 || info.uid !== process.getuid()) throw new Error('invalid marker');
    return JSON.parse(await file.readFile('utf8'));
  } finally { await file.close(); }
}
async function requestJSON(request) {
  let size = 0; const chunks = [];
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 4096) throw new Error('request too large');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString());
}

async function serve() {
  if (process.getuid() !== 1000) throw new Error('synthetic guest UID1000 required');
  const root = process.cwd();
  if (root !== '/home/sandbox/workspace/app') throw new Error('synthetic app path required');
  const manifest = JSON.parse(await fs.readFile('config/crash-fixture.json', 'utf8'));
  if (manifest.purpose !== 'DISPOSABLE_POSTGRES_CRASH_ONLY' || !hex.test(manifest.fixture)) throw new Error('owned fixture marker required');
  const tokenPath = path.join(root, 'crash-fixture-token');
  const tokenInfo = await fs.lstat(tokenPath);
  if (!tokenInfo.isFile() || tokenInfo.isSymbolicLink() || tokenInfo.uid !== 1000 || (tokenInfo.mode & 0o077)) throw new Error('private fixture capability required');
  const token = (await fs.readFile(tokenPath, 'utf8')).trim();
  if (!/^[a-f0-9]{64}$/.test(token)) throw new Error('invalid fixture capability');
  const {createDatabase} = await import('/opt/services/postgres/connection.mjs');
  const sql = createDatabase();
  const directories = [path.join(root, 'crash-fixture-data'), path.join(os.homedir(), '.cube-crash-fixture')];
  let busy = false;
  const server = createServer(async (request, response) => {
    const supplied = request.headers.authorization || '';
    const expected = `Bearer ${token}`;
    const authorized = /^Bearer [a-f0-9]{64}$/.test(supplied) && timingSafeEqual(Buffer.from(supplied), Buffer.from(expected));
    response.setHeader('content-type', 'application/json');
    response.setHeader('cache-control', 'no-store');
    if (!authorized) { response.writeHead(403); return response.end('{"error":"forbidden"}'); }
    if (request.method === 'GET' && request.url === '/status') {
      return response.end(JSON.stringify({fixture: manifest.fixture, ready: true}));
    }
    if (request.method !== 'POST' || !['/commit', '/verify'].includes(request.url)) {
      response.writeHead(404); return response.end('{"error":"unknown route"}');
    }
    if (busy) { response.writeHead(409); return response.end('{"error":"busy"}'); }
    busy = true;
    try {
      const marker = await requestJSON(request);
      if (!validateMarker(marker, manifest.fixture)) throw new Error('invalid marker');
      if (request.url === '/commit') {
        // No implicit/replayed commit on startup. The operator calls this once
        // before export, and once after export. Never call it after the crash.
        for (const directory of directories) await writeMarker(directory, marker);
        await sql.begin(async tx => {
          await tx`set local statement_timeout = '10s'`;
          await tx`set local synchronous_commit = on`;
          await tx`create table if not exists baarcha_crash_fixture (fixture text primary key, phase text not null, nonce text not null)`;
          const [settings] = await tx`select current_setting('fsync') as fsync, current_setting('full_page_writes') as full_page_writes, current_setting('synchronous_commit') as synchronous_commit`;
          if (Object.values(settings).some(value => value !== 'on')) throw new Error('durable PostgreSQL settings required');
          await tx`insert into baarcha_crash_fixture (fixture, phase, nonce) values (${marker.fixture}, ${marker.phase}, ${marker.nonce}) on conflict (fixture) do update set phase = excluded.phase, nonce = excluded.nonce`;
        });
      }
      const files = await Promise.all(directories.map(readMarker));
      const rows = await sql.begin(async tx => {
        await tx`set local statement_timeout = '10s'`;
        await tx`set local transaction_read_only = on`;
        return tx`select fixture, phase, nonce from baarcha_crash_fixture where fixture = ${manifest.fixture}`;
      });
      const matches = rows.length === 1 && files.every(value => sameMarker(value, marker)) && sameMarker(rows[0], marker);
      response.writeHead(matches ? 200 : 409);
      response.end(JSON.stringify({matches, app: files[0], home: files[1], database: rows[0] ?? null}));
    } catch {
      response.writeHead(500); response.end('{"error":"fixture verification failed"}');
    } finally { busy = false; }
  });
  server.requestTimeout = 15000;
  server.headersTimeout = 5000;
  server.maxConnections = 2;
  server.listen(3006, '0.0.0.0');
  for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => {
    server.close(async () => { await sql.end({timeout: 5}); process.exit(0); });
    setTimeout(() => process.exit(1), 7000).unref();
  });
}
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  serve().catch(() => { console.error('synthetic crash probe startup failed'); process.exitCode = 1; });
}
