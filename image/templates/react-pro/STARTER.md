# React Pro Base 2.0.0

This is a running application with editable page compositions. Adapt it to the
brief; do not reinstall the stack or rebuild existing infrastructure. The example
brand, plans and portfolio are illustrative. Replace them with the user's content
and remove sections outside the requested scope. A working example is not a
completed user request. Preserve the original request in `.baarcha/request.json`
when the platform supplies it; record adaptations and actual module use in BRIEF.md.

## Map

| Change | File |
| --- | --- |
| Selected page set | `src/content/starter.json` (`marketing`, `portfolio`, `app`) |
| Routing shell, lazy composition imports, error boundary | `src/App.tsx` |
| Marketing: `/`, `/pricing`, `/contact` | `src/pages/marketing/index.tsx` |
| Portfolio: `/`, `/work/:id`, `/contact` | `src/pages/portfolio/index.tsx` |
| App: `/`, `/projects`, `/projects/:id`, `/settings` | `src/pages/app/index.tsx` |
| Copy, brand, plans, portfolio records | `src/content/site.ts` |
| Asset URLs vs design references | `src/content/assets.ts` |
| Colors and light/dark values | `src/styles/theme.css` |
| Layout, typography, spacing, media baseline | `src/styles/base.css` |
| Tailwind imports and semantic token mapping | `src/index.css` |
| Accessible navigation, mobile dialog, footer | `src/components/layout/Shell.tsx` |
| Container, Section, Stack, Grid, VisuallyHidden | `src/components/layout/primitives.tsx` |
| Existing controls and their API reference | `src/components/ui/README.md` |
| Editable FAQ section | `src/sections/FAQ.tsx` |

Only the selected composition is routed. Others are separate lazy chunks. Add
custom sections freely; the examples do not constrain layout or art direction.
React Router 6 is installed for real URLs, history and links. For deployment the
static host must rewrite unknown application paths to index.html (Vite dev does).
Contact prepares a mailto draft; it does not send email. App records/settings are
React state and reset on reload; add real persistence only when requested. No
authentication, payment processing or database is implied by these pages.

## Installed helpers

- `Reveal`, `Stagger`, `StaggerItem` from `@/components/motion/Reveal`.
  Motion uses `motion/react`, not `framer-motion`. Reveal accepts duration/offset;
  MotionConfig and hooks honor reduced motion. Use Motion directly for presence,
  hover and scroll-progress interactions instead of another animation package.
- `BackgroundVideo` from `@/components/media/BackgroundVideo`: src, poster, label,
  type (`mp4` or `hls`), position. Muted inline playback, visible pause/play control,
  offscreen/background pause and static reduced-motion fallback. HLS uses native
  support first, otherwise lazy hls.js. Keep posters/local media in public/media.
- `ResponsiveImage`: normal image props plus required alt and optional fallback.
  `MediaFrame`: children, ratio (default `16 / 9`) and className.
- `useScrollScene(ref, setup)` from `@/modules/scroll/useScrollScene`: memoize setup
  with useCallback; GSAP + ScrollTrigger load only when used. Scope selectors to
  the ref. Context cleanup reverts timelines on unmount. No animations in reduced
  motion. Do not let Motion and GSAP own the same property.
- `useSmoothScroll(enabled)` from `@/modules/scroll/useSmoothScroll`: opt-in Lenis,
  mounted once; cleaned up on unmount, disabled for reduced motion.
- `Scene` from `@/modules/three/Scene`: label, fallback and memoized create(THREE).
  Return `{ scene, camera, frame(seconds), dispose() }`. Dispose your geometries,
  materials and textures. The wrapper handles renderer/RAF/listeners, caps pixel
  ratio at 1.5 and mounts lazily near the viewport. Supply a static fallback.
  Three.js is installed; React Three Fiber/Drei are not needed for these helpers.

Keep text in HTML. Website screenshots/recordings are composition references,
not content videos. Native MP4 playback needs no 3D or HLS renderer. Import only
the capabilities the requested design uses; optional packages are preinstalled
but do not enter the browser bundle unless imported. Use Tailwind v4's existing
Vite integration instead of copying legacy directives/configuration from prompts.

## Run and verify

The supervisor owns port 3000 and starts Vite. Edits hot-reload. `pnpm typecheck`
checks source; the platform runs `pnpm build` after tasks. `pnpm check:prepared`
verifies installed versions and available composition files without installing.
Build-time release checks also exercise all routes at desktop/mobile widths,
keyboard navigation, reduced motion, media failure and page interactions.
Hannibal owns live-preview screenshots during coding tasks; report ready screens
and continue independent work. Update AGENTS.md if stack or workflow facts change.

Library references: https://motion.dev/docs/react-installation and
https://github.com/video-dev/hls.js . Exact tested versions are in package.json
and pnpm-lock.yaml; preserve both and the Vite patch metadata together.
