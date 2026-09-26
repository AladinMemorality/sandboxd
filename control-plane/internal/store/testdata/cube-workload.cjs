'use strict';
// Synthetic fixture only: one allocation, fixed bounds, no outbound requests.
const http = require('node:http');
const crypto = require('node:crypto');
const { performance } = require('node:perf_hooks');
const MEMORY_BYTES = 512 * 1024 * 1024;
const CHUNK_BYTES = 4 * 1024 * 1024;
const LOAD_MS = 60000;
const nonce = crypto.randomBytes(16).toString('hex');
let memory = null, started = false, active = false, stopped = false, cycles = 0, checksum = 0;
let timer, offset = 0;
function stop() { stopped = true; active = false; clearInterval(timer); memory = null; if (typeof global.gc === 'function') global.gc(); }
function touchChunk() {
  if (stopped) return;
  const end = Math.min(offset + CHUNK_BYTES, MEMORY_BYTES);
  for (let i = offset; i < end; i += 4096) memory[i] = 90;
  offset = end;
  if (offset < MEMORY_BYTES) { setImmediate(touchChunk); return; }
  active = true;
  timer = setInterval(() => {
    const end = performance.now() + 5; // at most ~5% of one CPU per app
    while (performance.now() < end) checksum = (Math.imul(checksum + 1, 1664525) + 1013904223) >>> 0;
    cycles++;
  }, 100);
  setTimeout(stop, LOAD_MS).unref(); // never extends the absolute80s deadline
}
function sample() {
  let pages = 0;
  if (memory && active) for (let i = 0; i < memory.length; i += 4096) pages += memory[i];
  return { nonce, pid: process.pid, started, active, bytes: memory ? memory.length : 0,
    pages, cycles, checksum, rss: process.memoryUsage().rss, cpu: process.cpuUsage(),
    faults: { minor: process.resourceUsage().minorPageFault, major: process.resourceUsage().majorPageFault } };
}
const server = http.createServer((req, res) => {
  if (req.url === '/start' && req.method === 'POST' && !started) {
    started = true;
    memory = Buffer.allocUnsafeSlow(MEMORY_BYTES);
    setTimeout(stop, 80000).unref(); // absolute cap includes memory preparation
    setImmediate(touchChunk); // acknowledge first; touch4MiB per event-loop turn
  } else if (req.url !== '/health' || req.method !== 'GET') {
    res.writeHead(409); res.end(); return;
  }
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify(sample()));
});
server.listen(3000, '0.0.0.0');
setTimeout(() => { stop(); server.close(); process.exit(0); }, 8 * 60000).unref();
