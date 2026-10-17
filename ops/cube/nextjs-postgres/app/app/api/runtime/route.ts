import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createRequire } from 'node:module';
const exec = promisify(execFile);
const require = createRequire(import.meta.url);
export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';
export async function GET() {
  const version = async (binary: string) => (await exec(binary, ['--version'], {timeout: 30000})).stdout.trim();
  const [claude, postgresql, pnpm] = await Promise.all([
    version('/usr/local/bin/claude'), version('/opt/postgresql/bin/postgres'), version('/usr/local/bin/pnpm'),
  ]);
  return Response.json({node: process.versions.node, next: require('next/package.json').version,
    react: require('react/package.json').version, claude, postgresql, pnpm});
}
