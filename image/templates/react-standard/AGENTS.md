# This app

React 18 + Vite 5 + TypeScript single-page app. It starts as a tiny starter
in `src/App.tsx`; build what the task asks for inside `src/`.

## Starter state — skip discovery, start building

On a fresh project (no `BRAIN.md` yet) do NOT spend a turn reading files;
everything below is current, so your FIRST tool call can already write code:

- `index.html` — Vite entry, mounts `#root`, title placeholder to rename.
- `src/main.tsx` — mounts `<App />`; you never need to touch it.
- `src/App.tsx` — a "waiting" placeholder screen; replace it entirely.
- `src/index.css` — the design tokens + base classes listed below, already
  imported. `src/components/`, `src/lib/`, and `src/assets/` exist and are
  empty — write into them directly, no mkdir needed.
- No router, no state library, no test runner. Add one only when the task
  needs it (and record it here).

Emit independent file writes as parallel tool calls in one message, and put
the page shell (App + header + section placeholders) in the very first batch
so the preview shows structure within seconds.

## How it runs (platform-managed, do not fight it)

- A supervisor already runs the dev server: `vite --host 0.0.0.0 --port 3000`
  (declared in `sandbox.yaml`). Never start your own server on any port,
  never kill or wait on the supervisor's processes, and never change the
  port; your edits hot-reload into the live preview.
- This workspace IS a Vite + React app. Never convert it to another
  framework; a task that needs a different stack belongs in a different
  sandbox.
- Dependencies: the supervisor runs `pnpm install` on boot when needed. Run
  `pnpm install` yourself after editing `package.json`.
- Verify before finishing: `curl -s http://127.0.0.1:3000/` responds and
  `pnpm exec tsc --noEmit` is clean. Do not run `pnpm build` yourself; the
  platform runs it as the post-task check.
- Runtime errors from the user's browser land in `.runtime-errors.log`
  (one JSON line each, appended live by the dev server; wiring in
  `vite.config.ts` + `src/lib/report-errors.ts` — leave both alone). CHECK
  IT at task start and before finishing: it is the only way you can see
  the page breaking for the real user. Fix what it shows, then delete the
  file so stale entries don't mislead the next session.

## Show progress in the preview

The user watches the preview live while you work. Put the experience's
shell on screen in your first batch (navigation plus the primary view,
with designed placeholders), then flesh out one part at a time, saving as
you go so hot reload shows each step. Data and polish come after
something is visible.

## Layout

- `index.html` is the Vite entry; `src/main.tsx` mounts `src/App.tsx`;
  `src/index.css` holds global styles.
- No `public/` directory ships with the template; import your own assets from
  `src/` or inline them. The one exception is `public/media/`, where the
  platform puts the files the user attached (see Bridge below): Vite serves
  it at `/media/…`, so reference those by URL and leave them where they are.

## Design foundation (use it, do not rebuild it)

`src/index.css` ships design tokens and base components: tokens
(`--bg --card --ink --dim --line --accent --radius --font --font-display`)
and classes (`.container .section .kicker .btn .btn.ghost .card .grid.cols-2
.grid.cols-3 .dim`). Retheme by changing token values; compose pages from
these classes and add new ones alongside them instead of writing one-off
inline styles. `App.tsx` starts as a waiting screen; replace it entirely on
the first build.

## Design playbook

Build the actual usable experience as the first screen. When someone asks
for an app, tool, platform, or game, the first screen IS the app — never a
marketing page about it, and never in-app text explaining the app's own
features, sections, or shortcuts. A landing/marketing page is only the
deliverable when explicitly requested.

Match the design to the domain. SaaS, CRM, admin, and operational tools are
quiet, dense, and work-focused: organized information for scanning and
repeated action, restrained styling, predictable navigation — no oversized
heroes, no decorative card grids, no editorial composition. Games can be
expressive, animated, and playful. Brand, venue, product, and portfolio
pages are editorial and image-led.

Decide an art direction from the brief BEFORE writing components — a mood,
a palette, a type pairing — and retheme the tokens first. Then:

- Controls: icon buttons (with tooltips when unfamiliar) for tools,
  segmented controls for modes, toggles for binary settings,
  sliders/steppers for numbers, menus for option sets, tabs for views.
  Prefer a standard icon over a text pill (undo/redo arrows, B/I, save,
  zoom). Use lucide icons: `pnpm add lucide-react`, never hand-drawn SVG
  icons. Build the states a real user expects: hover, disabled, loading,
  and DESIGNED empty states.
- Cards: border radius 8px or less. Cards are ONLY for repeated items,
  modals, and genuinely framed tools — never cards inside cards, never
  page sections styled as floating cards. Sections are full-width bands
  with constrained inner content.
- Imagery: websites need real visual assets — request generated bitmap
  images through the bridge (below); never SVG illustrations or CSS-only
  art as primary media (custom SVG is for game assets). Images must show
  the actual product, place, object, or subject — not dark, blurred, or
  purely atmospheric filler.
- Heroes (only where a hero belongs): a real full-bleed image with text
  over it, NOT in a card; never a split text/media card layout, never a
  gradient or SVG hero. H1 = the brand/product/place name or literal
  offer; value props go in supporting copy. The next section must peek
  into the first viewport at every size.
- Color: no one-note palettes — if the page reads as mostly one hue family
  (all-purple, all-beige/cream/sand, all-slate, all-brown/orange), revise
  before finishing. Neutrals plus a real accent; text contrast ≥ 4.5:1.
- Typography: one display + one text face; display-scale type only in true
  heroes — compact panels, dashboards, and tools get smaller tight
  headings. Letter-spacing 0, never negative. Font size never scales with
  viewport width. Body measure 60–75ch.
- Layout stability: give boards, grids, toolbars, tiles, and icon buttons
  stable dimensions (aspect-ratio, grid tracks, min/max) so hover states
  or dynamic text can never shift the layout. Text must fit its container
  on every viewport (wrap, then shrink-to-fit the longest word) and
  NOTHING may overlap incoherently.
- No decorative orbs, gradient blobs, or bokeh. No Lorem ipsum, no emoji
  as icons: realistic copy in the product's language.
- Motion: 150–250ms ease on hover/reveal only; nothing loops.
- Domain logic with established rules (chess, physics, parsing): use a
  proven library rather than hand-rolling, unless asked.

## Bridge to the product chat (only when $BRIDGE_URL is set)

When the env vars `BRIDGE_URL` and `BRIDGE_TOKEN` exist, a chat assistant
sits between you and the user and can act for you. Call it like:

    curl -s -X POST "$BRIDGE_URL" -H "Authorization: Bearer $BRIDGE_TOKEN" \
      -H 'content-type: application/json' -d '{...}'

- Progress, at milestones (fire-and-forget):
  `{"kind":"report","text":"Shell and hero are live; menu grid next."}`
- A question the user should see: `{"kind":"question","text":"..."}` —
  never block waiting for an answer; finish what you can and state the open
  question in your final message too.
- Real images (much better than CSS-only placeholders):
  `{"kind":"image","prompt":"<art-directed English description: subject, setting, style, lighting, composition>","aspect_ratio":"16:9"}`
  returns `{"url":"..."}`. Download using that URL EXACTLY as returned
  (never rewrite its host: localhost points at your own container)
  (`curl -s --max-time 120 -o src/assets/<name>.png "<url>"`) and import
  the local file. Generation takes up to ~60s — use --max-time 120 and do
  NOT retry on your own: the server caches by prompt, so re-sending the
  identical prompt returns the SAME image (fast), and a changed prompt is
  a new paid generation. Budget AT MOST 8 generated images per task and
  reuse them across similar items; keep one consistent style and palette
  across every prompt; never ask for text in the image.
- The user's own files. A task may open with a list headed FILES THE USER
  ATTACHED: the platform has already written them into `public/media/` (with
  `public/media/manifest.json` describing each one) and Vite serves that
  folder at the site root, so reference them by URL: `<img src="/media/<name>">`,
  `<video src="/media/<name>" controls>`. Do not move, import, or re-encode
  them. They are the material the task is about: use each one where the
  task says, never a placeholder, a generated image, or a stock photo in its
  place, and never ask the user to upload them again. A video stays a video.
  For a file the task mentions that is not in the list, ask the bridge:
  `{"kind":"library"}` returns every file the user has uploaded, each with a
  `url` to curl into `public/media/`.
- Your last report, sent just before you finish, says in one line what is
  now on screen and anything you could not do. The user's assistant relays
  exactly that line, so never round up.

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

Model turns cost real seconds, so keep the turn count low. Building new:
write each file COMPLETE on the first pass and batch every independent
write of a stage into one message of parallel tool calls. Changing
existing code: small targeted edits, never whole-file rewrites. Keep
components in separate files so a change touches little, and verify once
at the end with the cheapest sufficient check.

## Skills (in .claude/skills — use them)

This workspace ships skills you discover natively: **self-screenshot**
(capture the live preview and LOOK at it with your vision — one atomic
capture→Read→delete cycle, at milestones and always before finishing design
work), **taste-redesign** / **taste-minimalist** (art-direction process),
**web-design-guidelines** (audit rules, vendored in its command.md), and
**design-inspiration** (real-site DESIGN.md references). For any visual
work: state a direction, build, self-screenshot, judge the pixels, fix.

## Session memory

Every task runs in a fresh agent session. Files are the only memory:

- `BRAIN.md` carries project state, decisions, and gotchas from earlier
  sessions. Read it before starting; append durable learnings before you
  finish.
- This file carries stable facts about the stack and workflow. If you change
  them (add a router, swap the styling approach, add a test runner), update
  this file so the next session starts right instead of rediscovering it.
