---
name: self-screenshot
description: SEE your own live preview with your vision. Use after every significant visual change, and always before declaring design work finished — judge the actual pixels, not your mental model of them.
---

# Self-screenshot: look at what you built

You can capture the live preview of THIS app and look at it. The platform
uses the same warmed capture service as Chat and returns a JPEG. Use this bridge
for screenshots; you do not need to install or launch a browser in this app.
Requires BRIDGE_URL and BRIDGE_TOKEN (present on platform tasks).

## The rule that makes this worth anything

A capture you do not LOOK at is a wasted capture. Every use of this skill is
one atomic cycle — all three steps, never just the first:

1. Capture and decode:

       curl -fsS -m 120 -X POST "$BRIDGE_URL" \
         -H "Authorization: Bearer $BRIDGE_TOKEN" -H 'content-type: application/json' \
         -d '{"kind":"screenshot","mode":"hero"}' \
         | python3 -c "import json,sys,base64;data=json.load(sys.stdin);print(' / '.join(data.get('warnings',[])),file=sys.stderr);open('/tmp/shot.jpg','wb').write(base64.b64decode(data['data_b64']))"

2. IMMEDIATELY Read /tmp/shot.jpg — in this same turn, before any other
   work. Write down what you actually see: spacing, contrast, alignment,
   overlap, anything mid-load or broken.

3. Delete it (`rm /tmp/shot.jpg`) so a stale frame can never be mistaken
   for a fresh look.

## When and how

- mode "hero" = the first 16:9 viewport (1280×720) — the default.
  mode "full" = the whole page, BUT sections sized in vh stretch in this
  mode; prefer hero, and use full only to check content exists below the
  fold, not to judge its proportions.
- A ready worker usually captures quickly; app startup or queued work can take
  longer. Use it at milestones (shell up, section done,
  before finishing) — roughly 3-5 cycles per design task, not after every
  edit.
- The service waits for initial rendering; do not add a fixed sleep before
  every capture. Read any returned warnings: missing live data makes the image
  incomplete. If hot reload is visibly unfinished, report that and capture again
  after the app is ready.
- Judge what you SEE against your stated art direction and the
  web-design-guidelines skill, fix what the pixels show, then look again.
