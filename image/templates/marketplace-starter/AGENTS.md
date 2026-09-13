# This app

A WORKING marketplace (React 18 + Vite + TypeScript + Tailwind v4 on the
component-kit base): listing grid with search/category/sort, detail dialog,
save-to-favorites, a validated posting form, and localStorage persistence,
already seeded with realistic data.

YOUR JOB IS TO CUSTOMIZE IT, NOT REBUILD IT. Most tasks are: rename the
brand (AppHeader), retheme the tokens (src/index.css), swap CATEGORIES and
the Listing fields (src/lib/types.ts), replace the seed data
(src/lib/seed.ts), fetch real photos via the bridge into listing.photo,
translate the strings, and add the one or two views the request actually
needs. Editing this app is minutes; regenerating it from scratch is many
minutes of wasted output. Bump the localStorage keys in src/lib/store.ts
("marketplace.listings.v1" → your app + .v2) whenever you change the
Listing shape or reseed, or returning browsers keep the old data.

## Task workflow

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

This describes the shipped template. Inspect the relevant current files before
editing; an existing or remixed workspace may already differ:

- `index.html` — Vite entry, mounts `#root`, title placeholder to rename.
- `src/main.tsx` — mounts `<App />` + the toast `<Toaster />`; leave it alone.
- `src/App.tsx` — the marketplace: view state, filtering, and composition.
- `src/components/` — AppHeader, FilterBar, ListingCard (+ListingPhoto),
  ListingDialog, NewListingDialog. `src/lib/` — types.ts (Listing +
  CATEGORIES), seed.ts, store.ts (the localStorage seam — swap for an API
  by reimplementing its five functions).
- `src/index.css` — Tailwind v4 + the design token contract (below).
- `src/components/ui/` — the PRE-BUILT component kit: button, card, input,
  label, textarea, select, dialog, dropdown-menu, tabs, badge, checkbox,
  switch, slider, table, tooltip, separator, skeleton, sonner (toasts).
  Import with `@/components/ui/<name>`; compose these before changing kit
  files without a task-specific reason; prefer shared variants and design tokens.
- Also preinstalled: `lucide-react` (icons), `recharts` (charts), `zod`
  (validation), `sonner` (`import { toast } from "sonner"`), `cn()` from
  `@/lib/utils`. Do not add another UI/icon/chart library.
- `src/components/` (app components), `src/lib/`, `src/hooks/`,
  `src/assets/` exist — write into them directly, no mkdir needed.
- No router or state library. For multi-view apps, hold a view value in
  App state and switch on it. Add a real router only if the task demands
  deep-linkable URLs (and record it here).

After understanding the scope, show a coherent shell early. Batch independent
operations and use focused edits to existing files.

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

Report the understood scope, then show a coherent primary view early and complete
the requested workflow. Keep milestone reports factual. Early preview progress
does not justify skipping discovery or guessing unresolved decisions.

## Layout

- `index.html` is the Vite entry; `src/main.tsx` mounts `src/App.tsx`;
  `src/index.css` holds global styles.
- No `public/` directory ships with the template; import your own assets from
  `src/` or inline them. The one exception is `public/media/`, where the
  platform puts the files the user attached (see Bridge below): Vite serves
  it at `/media/…`, so reference those by URL and leave them where they are.

## Design foundation (use it, do not rebuild it)

`src/index.css` defines the semantic token contract every kit component
consumes: background/foreground, card, popover, primary, secondary, muted,
accent, destructive, border/input/ring, radius, fonts — each with a light
value in `:root` and a dark value in `.dark`. RETHEME by editing those
variable values (both blocks) to the brief's palette; never restate colors
inside components. Style with Tailwind utilities referencing the tokens
(`bg-background text-foreground border-border bg-primary
text-muted-foreground` etc.), not hex values. Dark mode = the `.dark`
class on `<html>`; only build a toggle if the task wants one.

## Design playbook

Follow the user's requested design, references and existing product conventions.
These are defaults, not reasons to override the brief. Before visual edits,
inspect the existing view and design system and choose a coherent direction.
An app should open on its useful workflow; a marketing site should communicate
the requested offer. Do not replace a functioning app with a promotional page.

- Match layout and density to the domain: operational tools favor scanning and
  efficient actions; games and brand sites can be expressive when appropriate.
- Reuse the component kit and semantic design tokens. Customize shared variants
  when needed instead of scattering overrides. Keep unrelated styles intact.
- Use meaningful hierarchy, readable type and adequate contrast. Choose radii,
  colors, imagery and hero composition to fit the brief, not a universal recipe.
- Use the user's exact assets first. Request generated imagery through the bridge
  only when useful; do not replace supplied logos, photos or videos. Diagrams and
  code-native game visuals may use SVG/canvas when appropriate.
- Make layouts responsive and stable; text must fit without incoherent overlap.
  Include keyboard access, visible focus, accessible control names, and useful
  loading, empty and error states. Avoid motion that distracts from the task and
  respect reduced-motion preferences.
- Write realistic copy in the product's language. Do not invent business facts
  or present demo data, local storage or stub integrations as production systems.
- For public pages add relevant titles, descriptions, semantic structure and
  descriptive image text. Do not add unrelated SEO features to private tools.
- Use proven libraries for established domain logic when appropriate.
- Verify the main interaction and use self-screenshot for visual work: inspect
  the pixels, fix relevant problems and report what was actually checked.

## Bridge to the product chat (only when $BRIDGE_URL is set)

When the env vars `BRIDGE_URL` and `BRIDGE_TOKEN` exist, a chat assistant
sits between you and the user and can act for you. Call it like:

    curl -s -X POST "$BRIDGE_URL" -H "Authorization: Bearer $BRIDGE_TOKEN" \
      -H 'content-type: application/json' -d '{...}'

- Progress, at milestones (fire-and-forget):
  `{"kind":"report","text":"Shell and hero are live; menu grid next."}`
- A product question: `{"kind":"question","text":"..."}`.
  Ask only for a consequential decision you cannot discover from context.
  Continue independent work. If no independent work remains before an answer
  arrives, record the question and remaining work in BRIEF.md, report what needs
  input and end the task. A later update can resume; do not poll or guess.
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

Inspect before changing, batch independent operations, and use focused edits.
Keep components and modules small. Verify the requested behavior with sufficient
checks at meaningful stages; compilation alone does not prove feature completion.

## Skills (in .claude/skills — use them)

This workspace ships skills you discover natively: **self-screenshot**
(capture the live preview and LOOK at it with your vision — one atomic
capture→Read→delete cycle, at milestones and always before finishing design
work), **taste-redesign** / **taste-minimalist** (art-direction process),
**web-design-guidelines** (audit rules, vendored in its command.md), and
**design-inspiration** (real-site DESIGN.md references). For any visual
work: state a direction, build, self-screenshot, judge the pixels, fix.

## Session memory

New tasks start fresh; live follow-ups retain their current session. Keep durable
context in files so a later task can continue:

- `BRAIN.md` carries project state, decisions, and gotchas from earlier
  sessions. Read it before starting; append durable learnings before you
  finish.
- This file carries stable facts about the stack and workflow. If you change
  them (add a router, swap the styling approach, add a test runner), update
  this file so the next session starts right instead of rediscovering it.
