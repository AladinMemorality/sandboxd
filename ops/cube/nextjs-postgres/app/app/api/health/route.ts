import { sql } from '../../../lib/db';
export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';
export async function GET() {
  try {
    const [row] = await sql`select current_setting('server_version') as version`;
    return Response.json({status: 'ok', database: 'connected', postgresql: row.version});
  } catch {
    return Response.json({status: 'starting', database: 'unavailable'}, {status: 503});
  }
}
