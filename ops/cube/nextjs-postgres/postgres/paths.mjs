import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';

export function postgresPaths(home = os.homedir()) {
  const root = path.join(path.resolve(home), '.baarcha-postgres');
  const uid = process.getuid();
  const hash = createHash('sha256').update(root).digest('hex').slice(0, 20);
  return {root, data: path.join(root, 'data'), socket: `/tmp/baarcha-pg-${uid}-${hash}`, uid};
}

// A pre-existing symlink or another user's directory must never be adopted.
export function privateDirectory(directory) {
  try { fs.mkdirSync(directory, {mode: 0o700}); }
  catch (error) { if (error.code !== 'EEXIST') throw error; }
  const info = fs.lstatSync(directory);
  if (!info.isDirectory() || info.uid !== process.getuid()) {
    throw new Error('PostgreSQL state/socket path must be a directory owned by this guest user');
  }
  fs.chmodSync(directory, 0o700);
}

export function clusterState(directory) {
  const version = path.join(directory, 'PG_VERSION');
  try {
    const info = fs.lstatSync(version);
    if (!info.isFile() || info.uid !== process.getuid()) throw new Error('Invalid PostgreSQL version file');
    if (fs.readFileSync(version, 'utf8').trim() !== '18') {
      throw new Error('Existing PostgreSQL data needs a reviewed major-version migration');
    }
    return 'existing';
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    if (fs.readdirSync(directory).length !== 0) {
      throw new Error('Refusing to initialize an unrecognized nonempty PostgreSQL directory');
    }
    return 'empty';
  }
}
