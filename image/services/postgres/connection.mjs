import os from 'node:os';
import postgres from 'postgres';
import {postgresPaths} from './paths.mjs';

// Server-side only. No network address, password, or connection string is sent
// to the browser. Every guest has its own private Unix socket and data directory.
export function databaseConfig() {
  return {host: postgresPaths().socket, port: 5432, database: 'postgres', username: os.userInfo().username,
    max: 10, connect_timeout: 5, idle_timeout: 20};
}

export function createDatabase() { return postgres(databaseConfig()); }

export async function waitForDatabase(sql, timeoutMs = 60000) {
  const deadline = Date.now() + timeoutMs;
  while (true) {
    try { await sql`select 1`; return; }
    catch (error) {
      if (Date.now() >= deadline) throw new Error('Project PostgreSQL did not become ready', {cause: error});
      await new Promise(resolve => setTimeout(resolve, 200));
    }
  }
}
