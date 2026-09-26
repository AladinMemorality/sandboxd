import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import {spawn} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';
import {drainWorker} from './natural_drain.mjs';

const fixtureSource = `
import http from 'node:http';
import fs from 'node:fs/promises';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createRequire} from 'node:module';
const server = http.createServer(async (req, res) => {
  if(req.url === '/health') return res.end('ok');
  await fs.writeFile('started', 'yes');
  // Real subprocess and file I/O remain pending after the client disconnects.
  await promisify(execFile)(process.execPath, ['-e', 'setTimeout(()=>{}, 2000)']);
  if(process.env.CUBE_DRAIN_SHARP_PACKAGE) {
    const sharp = createRequire(process.env.CUBE_DRAIN_SHARP_PACKAGE)('sharp');
    const image = await sharp({create: {width: 512, height: 512, channels: 3, background: '#aaccee'}}).png().toBuffer();
    await fs.writeFile('completed.png', image);
  }
  const handle = await fs.open('completed', 'wx');
  await handle.writeFile('acknowledged tail bytes'); await handle.sync(); await handle.close();
  res.end('done');
});
server.listen(0, '127.0.0.1', () => console.log(JSON.stringify({port: server.address().port})));
`;

async function fixture(t) {
  const dir = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'cube-motion-drain-fixture-')));
  const file = path.join(dir, 'fixture.mjs'); await fs.writeFile(file, fixtureSource);
  const env = {PATH: process.env.PATH};
  if(process.env.CUBE_DRAIN_SHARP_PACKAGE) env.CUBE_DRAIN_SHARP_PACKAGE = process.env.CUBE_DRAIN_SHARP_PACKAGE;
  const child = spawn(process.execPath, ['--inspect-port=0', file], {cwd: dir, env, stdio: ['ignore', 'pipe', 'pipe']});
  let stderr = '', stdout = '';
  child.stderr.on('data', b => {stderr += b;}); child.stdout.on('data', b => {stdout += b;});
  const exited = new Promise(resolve => child.once('exit', (code, signal) => resolve({code, signal})));
  t.after(async () => {if(child.exitCode === null && child.signalCode === null) child.kill('SIGTERM'); await exited; await fs.rm(dir, {recursive: true, force: true});});
  const wait = async predicate => {for(let n=0;n<500;n++){if(await predicate())return;await delay(10);}throw Error('Fixture deadline exceeded');};
  await wait(() => stdout.includes('\n'));
  const port = JSON.parse(stdout.trim()).port;
  // Exercise Node's real on-demand inspector on this exact owned child.
  child.kill('SIGUSR1'); await wait(() => /ws:\/\/127\.0\.0\.1:(\d+)\//.test(stderr));
  const inspector = Number(/ws:\/\/127\.0\.0\.1:(\d+)\//.exec(stderr)[1]);
  const config = {version: 1, pid: child.pid, uid: process.getuid(), inspector_port: inspector,
    node_version: process.version, exec_path: process.execPath, cwd: dir,
    argv: [process.execPath, file], listeners: [`127.0.0.1:${port}`]};
  return {dir, child, exited, port, config, wait};
}

test('natural shutdown retains disconnected request tail until real I/O completes', {timeout: 15000}, async t => {
  const f = await fixture(t);
  const request = http.request({hostname: '127.0.0.1', port: f.port, path: '/write', method: 'POST'});
  request.on('error', () => {}); request.end();
  await f.wait(async () => {try{await fs.stat(path.join(f.dir, 'started'));return true;}catch{return false;}});
  request.destroy(); await delay(50);
  await assert.rejects(fs.stat(path.join(f.dir, 'completed')), {code: 'ENOENT'});
  const result = await drainWorker(f.config, true);
  assert.equal(result.close_requested, true); assert.equal(result.drained, false);
  assert.equal(result.process_exit_requested, false);
  assert.equal(f.child.exitCode, null, 'pending work must outlive listener close');
  assert.deepEqual(await f.exited, {code: 0, signal: null});
  assert.equal(await fs.readFile(path.join(f.dir, 'completed'), 'utf8'), 'acknowledged tail bytes');
  if(process.env.CUBE_DRAIN_SHARP_PACKAGE) {
    const image = await fs.readFile(path.join(f.dir, 'completed.png'));
    assert.equal(image.subarray(1, 4).toString(), 'PNG'); assert(image.length > 100);
  }
});

test('check inspects without closing the application and disables the inspector', {timeout: 15000}, async t => {
  const f = await fixture(t), result = await drainWorker(f.config);
  assert.equal(result.close_requested, false);
  assert.equal(await (await fetch(`http://127.0.0.1:${f.port}/health`)).text(), 'ok');
  await delay(250);
  await assert.rejects(fetch(`http://127.0.0.1:${f.config.inspector_port}/json/list`, {signal: AbortSignal.timeout(1000)}));
  assert.equal(f.child.exitCode, null);
});

test('idle target exits normally after acknowledging listener close', {timeout: 15000}, async t => {
  const f = await fixture(t), result = await drainWorker(f.config, true);
  assert.equal(result.close_requested, true);
  assert.deepEqual(await f.exited, {code: 0, signal: null});
});

for (const fault of ['pid', 'uid', 'listeners', 'argv', 'cwd']) {
  test(`wrong ${fault} refuses before closing the application`, {timeout: 15000}, async t => {
    const f = await fixture(t), config = {...f.config};
    if(fault === 'pid' || fault === 'uid') config[fault]++;
    else if(fault === 'listeners') config.listeners = ['127.0.0.1:1'];
    else if(fault === 'argv') config.argv = [process.execPath, '/other.mjs'];
    else config.cwd = '/';
    await assert.rejects(drainWorker(config, true));
    assert.equal(await (await fetch(`http://127.0.0.1:${f.port}/health`)).text(), 'ok');
    assert.equal(f.child.exitCode, null);
  });
}

test('an accepted client connection refuses close instead of aborting it', {timeout: 15000}, async t => {
  const f = await fixture(t);
  const request = http.request({hostname: '127.0.0.1', port: f.port, path: '/write', method: 'POST'});
  request.on('error', () => {}); request.end(); t.after(() => request.destroy());
  await f.wait(async () => {try{await fs.stat(path.join(f.dir, 'started'));return true;}catch{return false;}});
  await assert.rejects(drainWorker(f.config, true));
  assert.equal(f.child.exitCode, null);
});
