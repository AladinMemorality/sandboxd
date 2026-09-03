# This app

Python + FastAPI REST API served by uvicorn. It starts as a single `main.py`;
build what the task asks for from there.

## Starter state — skip discovery, start building

On a fresh project (no `BRAIN.md` yet) do NOT spend a turn reading files;
everything below is current, so your FIRST tool call can already write code:

- `main.py` — a minimal `app` with `GET /health` returning 200. Keep that
  route working forever; build everything else around it.
- `requirements.txt` — fastapi + uvicorn only. No database, no ORM.
- Split into modules (`models.py`, `routers/…`) as soon as the app grows
  past one screenful; keep `main.py` as the assembly point.

## API craft

- Pydantic models for every request/response body; let FastAPI generate the
  docs — a task is not done if `/docs` renders errors.
- Use proper status codes (201 on create, 404 via HTTPException, 422 comes
  free from validation) and a consistent error shape.
- State without a database: a module-level dict/list is fine for demo data,
  but say so in the response of `GET /health` or the docs description.
- If the task wants a UI, serve it from FastAPI itself (an `index.html` via
  `HTMLResponse` or StaticFiles) — same port, no second server.

## Bridge to the product chat (only when $BRIDGE_URL is set)

When the env vars `BRIDGE_URL` and `BRIDGE_TOKEN` exist, a chat assistant
sits between you and the user. POST JSON with
`Authorization: Bearer $BRIDGE_TOKEN`:
`{"kind":"report","text":"…"}` for milestone progress,
`{"kind":"question","text":"…"}` for something the user should decide
(never block on an answer), and
`{"kind":"image","prompt":"…","aspect_ratio":"16:9"}` returns
`{"url":"…"}` when the app serves HTML that needs a real image, and
`{"kind":"library"}` returns every file the user has uploaded, each with a
`url` to curl.

The user's own files: a task may open with a list headed FILES THE USER
ATTACHED, already written into `public/media/` with a `manifest.json` beside
them. Serve that folder as static files and use each file where the task says;
never a placeholder in its place, and a video stays a video. Your last report,
sent just before you finish, says in one line what now works and anything you
could not do; the user's assistant relays exactly that line, so never round
up.

## How it runs (platform-managed, do not fight it)

- A supervisor runs `uvicorn main:app --host 0.0.0.0 --port 3000 --reload`
  from the `.venv` virtualenv (declared in `sandbox.yaml`). Never start a
  second server and never change the port; `--reload` picks up your edits
  live.
- The app must keep answering `GET /health` with 200; the platform's
  readiness probe depends on it.
- Dependencies: install into the venv AND record them, in one move:
  `.venv/bin/pip install <pkg>` plus a line in `requirements.txt`. The venv is
  created from `requirements.txt` on first boot only, so an unrecorded
  dependency breaks the next sandbox clone.
- Verify before finishing: `curl -s http://127.0.0.1:3000/health` responds and
  `.venv/bin/python -m compileall -q .` is clean.

## Working style

Generation speed is the bottleneck in this environment: prefer small,
targeted edits over rewriting whole files, and keep code in separate modules
so a change touches little. Run the cheapest check that actually verifies
the change.

## Session memory

Every task runs in a fresh agent session. Files are the only memory:

- `BRAIN.md` carries project state, decisions, and gotchas from earlier
  sessions. Read it before starting; append durable learnings before you
  finish.
- This file carries stable facts about the stack and workflow. If you change
  them, update this file so the next session starts right instead of
  rediscovering it.
