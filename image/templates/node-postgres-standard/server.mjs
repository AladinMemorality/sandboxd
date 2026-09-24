import express from 'express';
import {fileURLToPath} from 'node:url';
import {createDatabase, waitForDatabase} from '/opt/services/postgres/connection.mjs';

const sql = createDatabase();
await waitForDatabase(sql);
await sql`create table if not exists notes (
  id bigint generated always as identity primary key,
  body text not null check (length(body) between 1 and 500),
  created_at timestamptz not null default now()
)`;
const app = express();
app.use(express.json({limit: '8kb'}));
app.get('/health', async (_request, response) => {
  await sql`select 1`;
  response.json({status: 'ok'});
});
app.get('/api/notes', async (_request, response) => {
  response.json(await sql`select id, body, created_at from notes order by id desc limit 100`);
});
app.post('/api/notes', async (request, response) => {
  const body = typeof request.body?.body === 'string' ? request.body.body.trim() : '';
  if (!body || body.length > 500) return response.status(400).json({error: 'Write a note of 1–500 characters.'});
  const [note] = await sql`insert into notes (body) values (${body}) returning id, body, created_at`;
  response.status(201).json(note);
});
app.use(express.static(fileURLToPath(new URL('./public', import.meta.url))));
app.use((error, _request, response, _next) => {
  console.error('Request failed', error.code || error.name);
  response.status(500).json({error: 'Could not complete this request.'});
});
const server = app.listen(3000, '0.0.0.0');
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => server.close(async () => { await sql.end({timeout: 5}); process.exit(0); }));
}
