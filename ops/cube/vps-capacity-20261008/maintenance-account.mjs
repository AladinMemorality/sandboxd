// Administrative runtime checks must not consume customers' daily allowance.
// Use the platform's account lock and accounting functions; preserve all usage
// before the lease and do not change entitlements or the daily usage ledger.
import assert from 'node:assert/strict';
import { listSandboxRuntimeInventory } from '/opt/baarcha/app/landing/src/lib/sandboxd/client.ts';
import { sandboxLimits } from '/opt/baarcha/app/landing/src/lib/projects/limits.ts';
import { recordRuntime, validRuntimeSample } from '/opt/baarcha/app/landing/src/lib/projects/runtime-usage.ts';
import { sql, closePg } from '/opt/baarcha/app/landing/src/lib/pg.ts';

const inspect = process.argv[2] === '--inspect';
const ids = process.argv.slice(inspect ? 3 : 2);
assert(ids.length > 0 && ids.length <= 50 && new Set(ids).size === ids.length);
assert(ids.every(id => /^[A-Z0-9]{26}$/.test(id)));
const selected = new Set(ids);
const rows = (await listSandboxRuntimeInventory()).filter(row => selected.has(row.id));
assert(rows.length === ids.length);
const groups = new Map();
for (const row of rows) {
  assert(/^baarcha:[1-9]\d*$/.test(row.owner));
  const owner = Number(row.owner.slice(8));
  if (!groups.has(owner)) groups.set(owner, []);
  groups.get(owner).push(row.id);
}
const scopes = await Promise.all([...groups].map(async ([owner, sandboxes]) => ({ owner, sandboxes, limits: await sandboxLimits(owner) })));
if (inspect) {
  console.log(JSON.stringify(scopes));
  await closePg();
  process.exit(0);
}
let finish;
const finished = new Promise(resolve => { finish = resolve; });
process.stdin.on('data', () => {});
process.stdin.on('end', finish);
process.stdin.resume();
const protectedScopes = scopes.filter(scope => scope.limits.runningSandboxes !== null || scope.limits.dailySeconds !== null);
async function checkpoint(scope, charge) {
  const current = (await listSandboxRuntimeInventory()).filter(row => scope.sandboxes.includes(row.id));
  assert(current.length === scope.sandboxes.length);
  for (const row of current) {
    assert(row.owner === `baarcha:${scope.owner}` && validRuntimeSample(row.accounting));
    await recordRuntime(scope.owner, row.id, row.accounting, charge);
  }
}
const held = [];
const connection = protectedScopes.length ? await sql().reserve() : null;
try {
  for (const scope of protectedScopes.sort((a, b) => a.owner - b.owner)) {
    const key = `sandbox-mutation:account:${scope.owner}`;
    const deadline = Date.now() + 30 * 60_000;
    for (;;) {
      const [row] = await connection`SELECT pg_try_advisory_lock(hashtextextended(${key},0)) AS locked`;
      if (row.locked) { held.push({ scope, key }); break; }
      assert(Date.now() < deadline, 'Account maintenance lock deadline exceeded');
      await new Promise(resolve => setTimeout(resolve, 500));
    }
    await checkpoint(scope, scope.limits.dailySeconds !== null);
  }
  console.log('MAINTENANCE_READY=' + JSON.stringify({ sandboxes: ids, protected_accounts: held.length }));
  await finished;
  for (const { scope } of held) await checkpoint(scope, false);
  console.log('MAINTENANCE_FINISHED=' + JSON.stringify({ sandboxes: ids, preexisting_usage_preserved: true, operator_time_not_charged: true }));
} finally {
  for (const { key } of held.reverse()) await connection`SELECT pg_advisory_unlock(hashtextextended(${key},0))`;
  connection?.release();
  await closePg();
}
