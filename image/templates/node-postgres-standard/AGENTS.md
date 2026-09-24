# This app

A full-stack starter with an editable frontend, Express API and persistent
PostgreSQL 18.4 database running privately inside this project's sandbox.

Understand the goal and scope before editing. Read relevant current files,
BRIEF.md and BRAIN.md when present; check whether the requested behavior already
exists. A starter map is navigation, not proof that files are unchanged. Batch
independent reads and keep discovery proportional to the task. For bugs, inspect
relevant logs and reproduce the failure before selecting a fix.

Clear implementation requests need no approval ritual. Ideas, reviews and
questions need discussion or inspection unless implementation is requested.
Choose sensible minor defaults and respect delegated choices. For consequential
unresolved decisions, use the bridge question workflow below and leave dependent
work unimplemented until answered. Keep BRIEF.md concise with product scope,
user decisions, assumptions, acceptance criteria and open questions; update it
when decisions change, not on every trivial edit. Keep technical gotchas in
BRAIN.md. A follow-up answer should resolve its question in the same project.

## Starter map

- `public/index.html`: frontend; customize its design and interactions.
- `server.mjs`: HTTP API, parameterized SQL and idempotent schema initialization.
- `sandbox.yaml`: platform supervisor starts the web process and database worker.
- `/opt/services/postgres/connection.mjs`: server-only database client.

## Runtime and persistence

The supervisor runs the web server on `0.0.0.0:3000`; preserve `/health` and do
not start a second server. Web code restarts after an agent task. The PostgreSQL
worker keeps running during ordinary code edits. The web process waits for SQL
readiness before accepting requests. Use parameterized queries and intentional,
non-destructive schema migrations; never reset an existing database to fix a
startup problem.

Database files live in `$HOME/.baarcha-postgres/data`, outside published source.
The Unix socket is private under `/tmp`, with no TCP database listener. Import
`createDatabase` from `/opt/services/postgres/connection.mjs` in server code.
Never expose connection details or database operations directly to the browser.

Stop/resume and owner source restore preserve the private database. Publication
shares code, not database contents; a remix initializes its own empty database.
For physical backup/migration stop writers and PostgreSQL, then preserve the
complete `.baarcha-postgres` directory including WAL with the owner home. Never
copy active PostgreSQL files or just its `base` subdirectory. A platform capture
or published snapshot is not a database backup. Generic logical export is not
provided by this image; do not claim `pg_dump` exists.

The notes example intentionally has no end-user account system. Add appropriate
application authentication and authorization when the requested product needs
private records or multiple users; project-preview authorization is separate.

## Agent tools and screenshots

Use the platform's existing scoped bridge tools and shared screenshot function
to inspect the page. Read shared `.claude/skills` when relevant to design or
browser verification. Do not put platform credentials into app files, frontend
code, logs or URLs. Persistence is an explicit product choice: add PostgreSQL to
another project only when its owner requests it.

## Bridge to the product chat (only when $BRIDGE_URL is set)

When the env vars `BRIDGE_URL` and `BRIDGE_TOKEN` exist, a chat assistant
sits between you and the user. POST JSON with
`Authorization: Bearer $BRIDGE_TOKEN`:
`{"kind":"report","text":"…"}` for milestone progress,
`{"kind":"question","text":"…"}` for something the user should decide
(continue only independent work; if blocked, checkpoint in BRIEF.md, report
what needs input and end the task for a later update), and `{"kind":"library"}` returns every file the
user has uploaded, each with a `url` to curl. A task may open with a list
headed FILES THE USER ATTACHED: those files are already in the workspace at
the listed paths (`public/media/`, with a `manifest.json`); use them where
the task says and never ask for them again. Your last report, sent just
before you finish, says in one line what now works and anything you could
not do; the user's assistant relays exactly that line, so never round up.

## Keys and secrets

An API key or any secret the app needs is declared, never written. Declare
it in `sandbox.yaml` under `env:` and read it from the environment on the
server side only:

    env:
      - name: OPENAI_API_KEY
        required: true
        hint: Used by the chat feature; from platform.openai.com

The platform stores the value the person enters (sealed, outside the
workspace) and starts the app with it as an environment variable. So: never
write a real value into any file, never ask for one through the bridge,
never put a secret in a `VITE_` / `NEXT_PUBLIC_` variable or anywhere the
browser loads, and build the feature so the app still runs, saying it needs
its key, until the value is set. A remix of the app carries the declaration
and never the value.

## Working style

Inspect before changing, batch independent operations, and use focused edits.
Keep components and modules small. Verify the requested behavior with sufficient
checks at meaningful stages; compilation alone does not prove feature completion.

## Session memory

New tasks start fresh; live follow-ups retain their current session. Keep durable
context in files so a later task can continue:

- `BRAIN.md` carries project state, decisions, and gotchas from earlier
  sessions. Read it before starting; append durable learnings before you
  finish.
- This file carries stable facts about the stack and workflow. If you change
  them, update this file so the next session starts right instead of
  rediscovering it.
