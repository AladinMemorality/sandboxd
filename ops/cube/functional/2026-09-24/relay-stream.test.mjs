import assert from 'node:assert/strict';
import http from 'node:http';
import test from 'node:test';
import {forwardBody} from './relay-stream.mjs';

test('caller cancellation observes the upstream stream abort without killing the relay', async () => {
  let aborted = false, observed;
  const closed = new Promise(resolve => { observed = resolve; });
  const server = http.createServer((req, res) => {
    if (req.url === '/health') { res.end('ready'); return; }
    let source;
    const body = new ReadableStream({start(controller) { source = controller; controller.enqueue(Buffer.from('first')); }});
    res.once('close', () => { aborted = true; source.error(new DOMException('Cancelled', 'AbortError')); observed(); });
    forwardBody(body, res);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const origin = `http://127.0.0.1:${server.address().port}`;
    await new Promise((resolve, reject) => {
      const request = http.get(origin, response => {
        response.once('data', () => { response.destroy(); resolve(); });
      });
      request.once('error', reject);
    });
    await closed;
    const response = await fetch(origin + '/health');
    assert.equal(await response.text(), 'ready');
    assert.equal(aborted, true);
  } finally { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
});
