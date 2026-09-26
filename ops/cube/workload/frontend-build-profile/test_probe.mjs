import assert from 'node:assert/strict';
import test from 'node:test';
import { matchingUpdate, percentile, validateInput, validateItems } from './probe.mjs';
const run = 'a'.repeat(16);
const root = `/home/sandbox/workspace/app/.operator-frontend-build-20260925/${run}/output`;
test('fixed output and loopback mode cannot name a customer directory or arbitrary probe', () => {
  assert.equal(validateInput('hmr', root + '/hmr.json', run, root + '/work'), root);
  assert.throws(() => validateInput('exec', root + '/hmr.json', run, root + '/work'));
  assert.throws(() => validateInput('hmr', '/home/sandbox/workspace/app/server.mjs', run, root + '/work'));
  assert.throws(() => validateInput('hmr', root + '/hmr.json', '../etc', root + '/work'));
});
test('HMR requires an actual update for the owned module, not merely websocket connection', () => {
  assert.equal(matchingUpdate({ type: 'connected' }), false);
  assert.equal(matchingUpdate({ type: 'update', updates: [{ path: '/unrelated.ts' }] }), false);
  assert.equal(matchingUpdate({ type: 'update', updates: [{ path: '/src/App.tsx' }] }), true);
});
test('API success requires exact synthetic records, not HTTP200 alone', () => {
  validateItems({ items: Array.from({ length: 256 }, (_, id) => ({ id, label: `Fixture item ${id}`, category: id % 5 })) });
  assert.throws(() => validateItems({ items: [] }));
  assert.throws(() => validateItems({ items: new Array(256).fill({ id: 0, label: 'wrong', category: 0 }) }));
  assert.equal(percentile([7, 1, 3, 9], .95), 9);
});
