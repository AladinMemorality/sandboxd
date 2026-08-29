---
name: self-screenshot
description: SEE your own live preview with your vision. Use after every significant visual change, and always before declaring design work finished — judge the actual pixels, not your mental model of them.
---

# Self-screenshot: look at what you built

You can capture the live preview of THIS app and look at it. The platform
takes the screenshot in a real browser and hands the PNG back. Requires the
BRIDGE_URL and BRIDGE_TOKEN env vars (present on platform tasks).

## The rule that makes this worth anything

A capture you do not LOOK at is a wasted capture. Every use of this skill is
one atomic cycle — all three steps, never just the first:

1. Capture and decode:

       curl -s -m 60 -X POST "$BRIDGE_URL" \
         -H "Authorization: Bearer $BRIDGE_TOKEN" -H 'content-type: application/json' \
         -d '{"kind":"screenshot","mode":"hero"}' \
         | python3 -c "import json,sys,base64;open('/tmp/shot.png','wb').write(base64.b64decode(json.load(sys.stdin)['data_b64']))"

2. IMMEDIATELY Read /tmp/shot.png — in this same turn, before any other
   work. Write down what you actually see: spacing, contrast, alignment,
   overlap, anything mid-load or broken.

3. Delete it (`rm /tmp/shot.png`) so a stale frame can never be mistaken
   for a fresh look.

## When and how

- mode "hero" = the first 16:9 viewport (1280×720) — the default.
  mode "full" = the whole page, BUT sections sized in vh stretch in this
  mode; prefer hero, and use full only to check content exists below the
  fold, not to judge its proportions.
- Capture takes 2-10s. Use it at milestones (shell up, section done,
  before finishing) — roughly 3-5 cycles per design task, not after every
  edit.
- Wait ~2s after a large edit so hot reload settles before capturing.
- Judge what you SEE against your stated art direction and the
  web-design-guidelines skill, fix what the pixels show, then look again.
