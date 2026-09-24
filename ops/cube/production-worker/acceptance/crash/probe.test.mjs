import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {validateMarker, sameMarker, writeMarker, readMarker} from './probe.mjs';

const fixture = 'a'.repeat(32);
const baseline = {fixture, phase: 'baseline', nonce: 'b'.repeat(32)};
const latest = {fixture, phase: 'latest', nonce: 'c'.repeat(32)};
test('a successful health response or baseline does not prove latest marker', () => {
  assert.equal(validateMarker(latest, fixture), true);
  assert.equal(validateMarker({...latest, sql: 'arbitrary'}, fixture), false);
  assert.equal(validateMarker(latest, 'd'.repeat(32)), false);
  assert.equal(validateMarker({...latest, phase: 'restore'}, fixture), false);
  assert.equal(sameMarker(baseline, latest), false);
  assert.equal(sameMarker({status: 'ok'}, latest), false);
  assert.equal(sameMarker({...latest, nonce: baseline.nonce}, latest), false);
  assert.equal(sameMarker({...latest}, latest), true);
});
test('atomic durable markers replace baseline and remain private', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'cube-crash-marker-'));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  const directory = path.join(root, 'owned');
  await writeMarker(directory, baseline);
  assert.deepEqual(await readMarker(directory), baseline);
  await writeMarker(directory, latest);
  assert.deepEqual(await readMarker(directory), latest);
  assert.equal((await fs.stat(path.join(directory, 'marker.json'))).mode & 0o777, 0o600);
  assert.deepEqual(await fs.readdir(directory), ['marker.json']);
});
test('marker reader/writer reject symlink substitution', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'cube-crash-symlink-'));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  const target = path.join(root, 'target');
  await fs.mkdir(target, {mode: 0o700});
  const directory = path.join(root, 'alias');
  await fs.symlink(target, directory);
  await assert.rejects(writeMarker(directory, latest), /private marker directory/);
  await fs.writeFile(path.join(root, 'unrelated'), 'preserve');
  await fs.symlink(path.join(root, 'unrelated'), path.join(target, 'marker.json'));
  await assert.rejects(readMarker(target));
  assert.equal(await fs.readFile(path.join(root, 'unrelated'), 'utf8'), 'preserve');
});
