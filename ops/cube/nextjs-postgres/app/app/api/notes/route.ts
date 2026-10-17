import { sql } from '../../../lib/db';
export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';
async function prepare() {
  await sql`create table if not exists starter_notes (
    id bigint generated always as identity primary key,
    body text not null check (length(body) between 1 and 500),
    created_at timestamptz not null default now()
  )`;
}
export async function GET() {
  await prepare();
  return Response.json(await sql`select id, body from starter_notes order by id desc limit 20`);
}
export async function POST(request: Request) {
  // The candidate preview is private. Keep browser writes same-origin as well.
  const origin = request.headers.get('origin');
  if (origin && new URL(origin).host !== request.headers.get('host')) {
    return Response.json({error: 'Invalid origin'}, {status: 403});
  }
  if (Number(request.headers.get('content-length') || 0) > 4096) return new Response(null, {status: 413});
  const text = await request.text();
  if (text.length > 4096) return new Response(null, {status: 413});
  let data;
  try { data = JSON.parse(text); } catch { return new Response(null, {status: 400}); }
  const body = typeof data?.body === 'string' ? data.body.trim() : '';
  if (!body || body.length > 500) return Response.json({error: 'Enter 1–500 characters.'}, {status: 400});
  await prepare();
  const [note] = await sql`insert into starter_notes (body) values (${body}) returning id, body`;
  return Response.json(note, {status: 201});
}
