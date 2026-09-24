import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {postgresPaths, privateDirectory, clusterState} from './paths.mjs';

test('data stays outside published workspace and socket outside owner-home exports', () => {
  const a = postgresPaths('/home/sandbox');
  const b = postgresPaths('/home/another');
  assert.equal(a.data, '/home/sandbox/.baarcha-postgres/data');
  assert.ok(a.socket.startsWith('/tmp/baarcha-pg-'));
  assert.notEqual(a.socket, b.socket);
  assert.ok(Buffer.byteLength(a.socket) < 90);
});

test('private directory rejects symlinks without changing their target', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'baarcha-pg-path-'));
  t.after(() => fs.rmSync(root, {recursive: true, force: true}));
  const target = path.join(root, 'target');
  fs.mkdirSync(target, {mode: 0o755});
  const link = path.join(root, 'link');
  fs.symlinkSync(target, link);
  assert.throws(() => privateDirectory(link), /must be a directory/);
  assert.equal(fs.statSync(target).mode & 0o777, 0o755);
});

test('only an empty directory is initialized; incompatible/unknown data survives untouched', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'baarcha-pg-state-'));
  t.after(() => fs.rmSync(root, {recursive: true, force: true}));
  privateDirectory(root);
  assert.equal(fs.statSync(root).mode & 0o777, 0o700);
  assert.equal(clusterState(root), 'empty');
  fs.writeFileSync(path.join(root, 'owner-file'), 'preserve');
  assert.throws(() => clusterState(root), /nonempty/);
  fs.writeFileSync(path.join(root, 'PG_VERSION'), '17\n');
  assert.throws(() => clusterState(root), /major-version/);
  fs.writeFileSync(path.join(root, 'PG_VERSION'), '18\n');
  assert.equal(clusterState(root), 'existing');
  assert.equal(fs.readFileSync(path.join(root, 'owner-file'), 'utf8'), 'preserve');
});
