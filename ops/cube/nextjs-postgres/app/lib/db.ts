import 'server-only';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import postgres from 'postgres';
const state = globalThis as typeof globalThis & { database?: ReturnType<typeof postgres> };
const root = path.join(path.resolve(os.homedir()), '.baarcha-postgres');
const hash = createHash('sha256').update(root).digest('hex').slice(0, 20);
export const sql = state.database ??= postgres({
  host: `/tmp/baarcha-pg-${process.getuid!()}-${hash}`,
  port: 5432, database: 'postgres', username: os.userInfo().username,
  max: 5, connect_timeout: 3, idle_timeout: 20,
});
