import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import postgres from 'postgres';
import {postgresPaths} from './paths.mjs';

test('real PostgreSQL preserves committed data across crash/restart and cold backup; a fresh project is empty', {timeout: 90000}, async t => {
  const homes = [fs.mkdtempSync(path.join(os.tmpdir(), 'project-pg-owner-')), fs.mkdtempSync(path.join(os.tmpdir(), 'project-pg-remix-'))];
  const backup = fs.mkdtempSync(path.join(os.tmpdir(), 'project-pg-backup-'));
  let worker;
  let sql;
  let logs = '';
  let activeHome;
  let spawnError;
  const killPostmaster = () => {
    // The PID comes only from this test's fresh, owned cluster, never an
    // operator-selected or production PGDATA. PostgreSQL may start its own
    // process group, so killing the Node group alone is not a database crash.
    const pidFile = path.join(postgresPaths(activeHome).data, 'postmaster.pid');
    if (!fs.existsSync(pidFile)) return;
    const pid = Number(fs.readFileSync(pidFile, 'utf8').split('\n')[0]);
    assert.ok(Number.isInteger(pid) && pid > 1);
    try { process.kill(pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
  };
  const stop = async signal => {
    if (!worker || worker.exitCode !== null || worker.signalCode !== null) return;
    const exit = once(worker, 'exit');
    if (signal === 'SIGKILL') killPostmaster();
    else worker.kill('SIGTERM');
    let timer;
    const timeout = new Promise((_, reject) => {
      timer = setTimeout(() => {
        killPostmaster();
        try { process.kill(-worker.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') reject(error); }
        reject(new Error('Synthetic PostgreSQL worker did not stop within 10 seconds'));
      }, 10000);
    });
    try { await Promise.race([exit, timeout]); } finally { clearTimeout(timer); }
    worker = undefined;
  };
  t.after(async () => {
    if (sql) await sql.end({timeout: 1});
    await stop('SIGTERM');
    for (const home of homes) {
      fs.rmSync(postgresPaths(home).socket, {recursive: true, force: true});
      fs.rmSync(home, {recursive: true, force: true});
    }
    fs.rmSync(backup, {recursive: true, force: true});
  });
  const start = async home => {
    logs = '';
    activeHome = home;
    spawnError = undefined;
    worker = spawn(process.execPath, [fileURLToPath(new URL('./worker.mjs', import.meta.url))], {env: {...process.env, HOME: home}, detached: true, stdio: ['ignore', 'pipe', 'pipe']});
    worker.once('error', error => { spawnError = error; });
    for (const stream of [worker.stdout, worker.stderr]) stream.on('data', data => { logs = (logs + data).slice(-12000); });
    sql = postgres({host: postgresPaths(home).socket, username: os.userInfo().username, database: 'postgres', max: 1, connect_timeout: 1});
    const deadline = Date.now() + 30000;
    while (true) {
      try { await sql`select 1`; return; }
      catch {
        if (spawnError || worker.exitCode !== null || worker.signalCode !== null || Date.now() > deadline) throw Error('Synthetic PostgreSQL worker failed: ' + (spawnError?.code || logs));
        await new Promise(resolve => setTimeout(resolve, 100));
      }
    }
  };
  await start(homes[0]);
  assert.equal((await sql`show listen_addresses`)[0].listen_addresses, '');
  assert.equal((await sql`show server_version`)[0].server_version, '18.4');
  await sql`create table recovery_notes (body text not null)`;
  await sql`insert into recovery_notes values ('committed before crash')`;
  await sql.end({timeout: 1}); sql = undefined;
  await stop('SIGKILL');
  await start(homes[0]);
  assert.equal((await sql`select body from recovery_notes`)[0].body, 'committed before crash');
  await sql.end({timeout: 1}); sql = undefined;
  await stop('SIGTERM');
  assert.equal(fs.existsSync(path.join(postgresPaths(homes[0]).data, 'postmaster.pid')), false);
  fs.cpSync(postgresPaths(homes[0]).root, path.join(backup, 'cluster'), {recursive: true});
  fs.rmSync(postgresPaths(homes[0]).root, {recursive: true});
  fs.cpSync(path.join(backup, 'cluster'), postgresPaths(homes[0]).root, {recursive: true});
  await start(homes[0]);
  assert.equal((await sql`select body from recovery_notes`)[0].body, 'committed before crash');
  await sql.end({timeout: 1}); sql = undefined;
  await stop('SIGTERM');
  await start(homes[1]);
  assert.equal((await sql`select to_regclass('public.recovery_notes') as relation`)[0].relation, null);
});
