const fs = require('node:fs');
const path = require('node:path');
const { DatabaseSync } = require('node:sqlite');
const result = { files: 0, bytes: 0, gitFiles: 0, uploads: 0, localEnv: 0, databases: [] };
function walk(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) { walk(full); continue; }
    if (!entry.isFile()) continue;
    const size = fs.statSync(full).size;
    result.files++; result.bytes += size;
    result.gitFiles += Number(full.includes('/.git/'));
    result.uploads += Number(full.includes('/uploads/'));
    result.localEnv += Number(entry.name === '.env' || entry.name.startsWith('.env.'));
    if (!/\.(db|sqlite|sqlite3)$/.test(entry.name)) continue;
    const fd = fs.openSync(full, 'r'); const header = Buffer.alloc(16);
    fs.readSync(fd, header, 0, 16, 0); fs.closeSync(fd);
    if (header.toString() !== 'SQLite format 3\0') {
      result.databases.push({ type: 'other', bytes: size }); continue;
    }
    const db = new DatabaseSync(full, { readOnly: true });
    try {
      const checks = db.prepare('PRAGMA integrity_check').all();
      if (checks.length !== 1 || checks[0].integrity_check !== 'ok') throw Error('SQLite integrity check failed');
      const tables = db.prepare("SELECT count(*) AS n FROM sqlite_master WHERE type='table'").get().n;
      result.databases.push({ type: 'sqlite', integrity: 'ok', tables, bytes: size });
    } finally { db.close(); }
  }
}
walk('/restore');
console.log(JSON.stringify(result));
