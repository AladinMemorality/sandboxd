// Operator-only bridge for the pre-UDS worker, which has no shutdown handler.
// The caller owns traffic/writer fences and the exact Linux process generation.
// This module never opens an inspector, signals a process, exits the target,
// cancels a request/job, writes target files or restarts a service.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {pathToFileURL} from 'node:url';

export async function drainWorker(c, execute = false) {
  assert.equal(c.version, 1);
  assert(Number.isSafeInteger(c.pid) && c.pid > 1);
  assert(Number.isSafeInteger(c.uid) && c.uid >= 0);
  assert(Number.isSafeInteger(c.inspector_port) && c.inspector_port > 0 && c.inspector_port < 65536);
  assert.equal(c.node_version, process.version);
  assert(typeof c.exec_path === 'string' && c.exec_path.startsWith('/'));
  assert(typeof c.cwd === 'string' && c.cwd.startsWith('/'));
  assert(Array.isArray(c.argv) && c.argv.length === 2 && c.argv.every(v => typeof v === 'string'));
  assert(Array.isArray(c.listeners) && c.listeners.length > 0 && c.listeners.length <= 2);
  assert(c.listeners.every(v => typeof v === 'string'));
  const origin = `http://127.0.0.1:${c.inspector_port}`;
  const response = await fetch(origin + '/json/list', {redirect: 'error', signal: AbortSignal.timeout(5000)});
  assert.equal(response.status, 200);
  const raw = await response.text(); assert(raw.length < 65536);
  const targets = JSON.parse(raw); assert.equal(targets.length, 1); assert.equal(targets[0].type, 'node');
  const url = new URL(targets[0].webSocketDebuggerUrl);
  assert.equal(url.protocol, 'ws:'); assert.equal(url.hostname, '127.0.0.1');
  assert.equal(url.port, String(c.inspector_port)); assert.equal(url.username + url.password + url.search + url.hash, '');
  assert.match(url.pathname, /^\/[a-f0-9-]{36}$/);
  const ws = new WebSocket(url); let sequence = 0; const pending = new Map();
  ws.addEventListener('message', event => {
    if (typeof event.data !== 'string' || event.data.length > 65536) { ws.close(); return; }
    const value = JSON.parse(event.data), item = pending.get(value.id);
    if (!item) return;
    pending.delete(value.id); clearTimeout(item.timer);
    if (value.error || value.result?.exceptionDetails) item.reject(Error('Target inspection refused'));
    else item.resolve(value.result);
  });
  ws.addEventListener('close', () => {
    for (const item of pending.values()) { clearTimeout(item.timer); item.reject(Error('Inspector closed before acknowledgement')); }
    pending.clear();
  });
  async function evaluate(expression) {
    const id = ++sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(Error('Inspector command not acknowledged')); }, 5000);
      pending.set(id, {resolve, reject, timer});
      ws.send(JSON.stringify({id, method: 'Runtime.evaluate', params: {expression, awaitPromise: true, returnByValue: true}}));
    });
  }
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error('Inspector connection timed out')), 5000);
    ws.addEventListener('open', () => { clearTimeout(timer); resolve(); }, {once: true});
    ws.addEventListener('error', () => { clearTimeout(timer); reject(Error('Inspector connection failed')); }, {once: true});
  });
  let acknowledged = false;
  try {
    // Do not return handles, request payloads, environment, project state or keys.
    const result = await evaluate(`(async () => {
      const wanted = ${JSON.stringify(c)}, execute = ${JSON.stringify(execute)};
      const need = process.getBuiltinModule('node:assert/strict');
      need.equal(process.pid, wanted.pid); need.equal(process.getuid(), wanted.uid);
      need.equal(process.version, wanted.node_version); need.equal(process.execPath, wanted.exec_path);
      need.equal(process.cwd(), wanted.cwd); need.deepEqual(process.argv, wanted.argv);
      need(process.exitCode === undefined || process.exitCode === 0);
      const http = process.getBuiltinModule('node:http');
      const servers = process._getActiveHandles().filter(h => h instanceof http.Server && h.listening);
      const label = s => {const a = s.address(); return typeof a === 'string' ? a : a.address + ':' + a.port;};
      need.deepEqual(servers.map(label).sort(), [...wanted.listeners].sort());
      for (const server of servers) {
        const connections = await new Promise((resolve, reject) => server.getConnections((e, n) => e ? reject(e) : resolve(n)));
        need.equal(connections, 0);
      }
      // Recheck all identities before the first mutation. No connection/timeout
      // is destroyed, and no process.exit is used. Native async work stays live.
      need.deepEqual(servers.map(label).sort(), [...wanted.listeners].sort());
      // Schedule debugger cleanup before closing the last listener. An idle
      // target can retire its execution context immediately after this result.
      setTimeout(() => process.getBuiltinModule('node:inspector').close(), 100).unref();
      if (execute) for (const server of servers) server.close();
      return {version: 1, pid: process.pid, listeners: wanted.listeners, close_requested: execute,
              process_exit_requested: false, drained: false};
    })()`);
    assert.equal(result.result?.type, 'object');
    acknowledged = true;
    return result.result.value;
  } finally {
    // A short unref'ed timer lets the acknowledgement reach this client before
    // inspector.close waits for its disconnection. It cannot terminate work.
    try { if (!acknowledged) await evaluate("setTimeout(() => process.getBuiltinModule('node:inspector').close(), 100).unref(); true"); }
    finally { ws.close(); }
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  assert(process.argv.length === 3 || (process.argv.length === 4 && process.argv[3] === '--execute'));
  const file = process.argv[2], info = await fs.lstat(file);
  assert.equal(process.getuid(), 0); assert(info.isFile() && !info.isSymbolicLink());
  assert.equal(info.uid, 0); assert.equal(info.mode & 0o777, 0o600); assert(info.size < 16384);
  const result = await drainWorker(JSON.parse(await fs.readFile(file)), process.argv[3] === '--execute');
  console.log(JSON.stringify(result));
}
