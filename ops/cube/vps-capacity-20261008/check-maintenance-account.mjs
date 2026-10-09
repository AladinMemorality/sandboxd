import assert from 'node:assert/strict';
import { sql, closePg } from '/opt/baarcha/app/landing/src/lib/pg.ts';
const held = process.argv[2] === 'held';
const owners = [5, 13, 25];
const result = [];
try {
  const db = sql();
  for (const owner of owners) {
    const key = `sandbox-mutation:account:${owner}`;
    const connection = await db.reserve();
    try {
      const [row] = await connection`SELECT pg_try_advisory_lock(hashtextextended(${key},0)) AS locked`;
      if (row.locked) await connection`SELECT pg_advisory_unlock(hashtextextended(${key},0))`;
      assert.equal(row.locked, !held);
      const usage = await connection`SELECT day::text,seconds::text FROM sandbox_daily_usage WHERE owner_id=${owner} ORDER BY day`;
      result.push({ owner, lock_blocked: !row.locked, usage });
    } finally { connection.release(); }
  }
  console.log(JSON.stringify(result));
} finally { await closePg(); }
