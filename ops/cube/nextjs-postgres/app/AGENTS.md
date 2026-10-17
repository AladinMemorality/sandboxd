# Prepared Next.js + PostgreSQL sandbox

Next.js 16.4.2 App Router, React/React DOM 19.3.1, TypeScript 7.0.2,
Tailwind 4.3.3. Read relevant guides in node_modules/next/dist/docs before editing.
Use this prepared app directly. Do not create another app or replace the framework.

The platform supervises `pnpm dev` on port 3000 and a private PostgreSQL 18.6 worker.
Do not start duplicate servers, kill supervisor processes or expose PostgreSQL.
`pnpm build` uses `.next-build`, separate from the `.next` development cache.
Never clear development caches routinely. Validate with `pnpm typecheck`,
`pnpm build`, and relevant HTTP/browser checks.

`app/` is the App Router; interactive components declare `use client`.
`lib/db.ts` is server-only and exports the prepared SQL client. SQL values must
use parameterized tagged templates. Database files live outside source exports
at ~/.baarcha-postgres, with a private Unix socket; no TCP database port is open.
The note form is only a private sandbox example, not a production account system.
Implement authentication/authorization before publishing private user data.
Use migrations for real schemas. Do not put secrets in NEXT_PUBLIC_ variables.

Dependencies are installed and pinned in package.json and pnpm-lock.yaml.
Add packages only when the task needs them, and preserve exact framework pins.
Use the existing design skills in .claude/skills for visual work. Follow the
selected prompt's actual assets, layout, typography and motion requirements.
