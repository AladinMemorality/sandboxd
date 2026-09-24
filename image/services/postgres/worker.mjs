import {createRequire} from 'node:module';
import {spawn, execFileSync} from 'node:child_process';
import os from 'node:os';
import {postgresPaths, privateDirectory, clusterState} from './paths.mjs';

const require = createRequire(import.meta.resolve('embedded-postgres'));
const binary = await import(require.resolve(`@embedded-postgres/${process.platform}-${process.arch}`));
const version = execFileSync(binary.postgres, ['--version'], {encoding: 'utf8', timeout: 10000}).trim();
if (!/\(PostgreSQL\) 18\.4(?:\s|$)/.test(version)) throw new Error('Reviewed PostgreSQL 18.4 binaries required');
const paths = postgresPaths();
privateDirectory(paths.root);
privateDirectory(paths.data);
privateDirectory(paths.socket);
let child;
let stopping = false;
for (const signal of ['SIGTERM', 'SIGINT']) {
  process.on(signal, () => { stopping = true; child?.kill('SIGINT'); });
}
async function run(program, args) {
  return new Promise((resolve, reject) => {
    child = spawn(program, args, {stdio: 'inherit'});
    child.once('error', reject);
    child.once('exit', (code, signal) => {
      child = undefined;
      if (code === 0 || stopping) resolve();
      else reject(new Error(`PostgreSQL process exited (${code ?? signal})`));
    });
  });
}
if (clusterState(paths.data) === 'empty') {
  await run(binary.initdb, ['-D', paths.data, '-U', os.userInfo().username,
    '--auth-local=trust', '--auth-host=reject', '--encoding=UTF8', '--locale=C']);
}
if (!stopping) {
  await run(binary.postgres, ['-D', paths.data, '-h', '', '-k', paths.socket,
    '-c', 'max_connections=40', '-c', 'shared_buffers=32MB', '-c', 'unix_socket_permissions=0700']);
}
